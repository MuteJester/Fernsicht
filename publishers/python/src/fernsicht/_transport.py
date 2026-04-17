"""Background WebRTC sender transport for Fernsicht."""

from __future__ import annotations

import asyncio
import json
import logging
import threading
import time
from dataclasses import dataclass, field
from queue import Empty, Queue
from typing import Any

import aiohttp
from aiortc import (
    RTCConfiguration,
    RTCDataChannel,
    RTCIceCandidate,
    RTCIceServer,
    RTCPeerConnection,
    RTCSessionDescription,
)
from aiortc.sdp import candidate_from_sdp, candidate_to_sdp

from fernsicht._wire import (
    serialize_end,
    serialize_identity,
    serialize_keepalive,
    serialize_progress,
    serialize_start,
)

logger = logging.getLogger("fernsicht")

PUBLISH_INTERVAL = 0.5
HEARTBEAT_INTERVAL = 5.0
SHUTDOWN_GRACE_SEC = 2.0
LEGACY_VIEWER_ID = "__legacy__"

STUN_SERVERS = [
    RTCIceServer(urls=["stun:stun.l.google.com:19302"]),
    RTCIceServer(urls=["stun:stun1.l.google.com:19302"]),
]


@dataclass(slots=True)
class ViewerPeerState:
    viewer_id: str
    pc: RTCPeerConnection
    channel: RTCDataChannel
    channel_open: asyncio.Event = field(default_factory=asyncio.Event)
    pending_remote_ice: list[RTCIceCandidate] = field(default_factory=list)
    remote_description_set: bool = False
    session_frames_sent: bool = False
    pending_outbound: list[str] = field(default_factory=list)
    last_send: float = field(default_factory=time.monotonic)
    disconnected_since: float | None = None


class Transport:
    """Publishes progress updates over WebRTC DataChannel on a background thread."""

    def __init__(
        self,
        room_id: str,
        *,
        start_time: float,
        desc: str | None,
        total: int | None,
        unit: str,
        signaling_url: str,
        sender_token: str | None = None,
    ) -> None:
        self._room_id = room_id
        self._task_id = room_id[:8]
        self._peer_id = f"py-{room_id[:8]}"
        self._signaling_url = signaling_url
        self._sender_token = sender_token.strip() if sender_token else None
        self._start_time = start_time

        self._desc = desc
        self._total = total
        self._unit = unit

        # Latest state snapshot from user thread.
        self._lock = threading.Lock()
        self._latest: dict[str, Any] | None = None
        self._snapshot: dict[str, Any] | None = None

        self._outbound_immediate: Queue[str] = Queue()
        self._closed = False
        self._stop_event = threading.Event()

        self._thread = threading.Thread(
            target=self._run,
            name=f"fernsicht-{room_id[:8]}",
            daemon=True,
        )
        self._thread.start()

    def post(
        self,
        *,
        n: int,
        total: int | None,
        desc: str | None,
        unit: str,
        rate: float | None,
        elapsed: float,
    ) -> None:
        """Update the latest state. Called from the user's thread."""
        with self._lock:
            state = {
                "n": n,
                "total": total,
                "desc": desc,
                "unit": unit,
                "rate": rate,
                "elapsed": elapsed,
            }
            self._latest = state
            self._snapshot = state

    def send_error(self, *, error: str, message: str, fatal: bool) -> None:
        """Record an error and end the current task on all connected viewers."""
        logger.warning("Publisher error: %s: %s (fatal=%s)", error, message, fatal)
        self._outbound_immediate.put(serialize_end(self._task_id))

    def close(
        self,
        *,
        n: int,
        total: int | None,
        desc: str | None,
        unit: str,
        rate: float | None,
        elapsed: float,
    ) -> None:
        """Send a terminal message and shut down the background thread."""
        if self._closed:
            return
        self._closed = True
        self._outbound_immediate.put(serialize_end(self._task_id))
        self._stop_event.set()
        self._thread.join(timeout=5.0)

    def _run(self) -> None:
        try:
            asyncio.run(self._run_async())
        except Exception:
            logger.exception("WebRTC transport thread crashed")

    async def _run_async(self) -> None:
        viewer_peers: dict[str, ViewerPeerState] = {}
        pending_broadcast: list[str] = []
        last_tick = time.monotonic()
        shutdown_deadline: float | None = None

        async with aiohttp.ClientSession() as session:
            async with session.ws_connect(
                self._signaling_url,
                heartbeat=30.0,
                autoping=True,
                max_msg_size=1_048_576,
            ) as ws:
                await ws.send_str(self._build_join_message())

                while True:
                    try:
                        sig_msg = await ws.receive(timeout=0.2)
                    except asyncio.TimeoutError:
                        sig_msg = None

                    if sig_msg is not None:
                        should_stop = await self._handle_signaling_message(
                            sig_msg=sig_msg,
                            ws=ws,
                            viewer_peers=viewer_peers,
                        )
                        if should_stop:
                            break

                    # Drain immediate queue.
                    while True:
                        try:
                            pending_broadcast.append(self._outbound_immediate.get_nowait())
                        except Empty:
                            break

                    now = time.monotonic()
                    if (now - last_tick) >= PUBLISH_INTERVAL:
                        last_tick = now
                        progress = self._consume_latest_progress()
                        if progress is not None:
                            pending_broadcast.append(progress)

                    stale_viewer_ids = [
                        viewer_id
                        for viewer_id, state in viewer_peers.items()
                        if (
                            state.pc.connectionState in {"failed", "closed"}
                            or (
                                state.pc.connectionState == "disconnected"
                                and state.disconnected_since is not None
                                and (now - state.disconnected_since) >= 20.0
                            )
                        )
                    ]
                    for viewer_id in stale_viewer_ids:
                        await self._close_viewer_peer(viewer_peers.pop(viewer_id))

                    for state in viewer_peers.values():
                        channel_ready_now = (
                            state.channel_open.is_set()
                            and state.channel.readyState == "open"
                        )
                        if not channel_ready_now:
                            continue

                        if not state.session_frames_sent:
                            state.pending_outbound.append(serialize_identity(self._peer_id))
                            state.pending_outbound.append(
                                serialize_start(self._task_id, self._desc or "Task")
                            )
                            snapshot = self._snapshot_progress()
                            if snapshot is not None:
                                state.pending_outbound.append(snapshot)
                            state.session_frames_sent = True

                        if pending_broadcast:
                            state.pending_outbound.extend(pending_broadcast)
                        elif (now - state.last_send) >= HEARTBEAT_INTERVAL:
                            state.pending_outbound.append(serialize_keepalive())

                        still_pending: list[str] = []
                        for frame in state.pending_outbound:
                            if self._try_send_frame(state.channel, frame):
                                state.last_send = time.monotonic()
                            else:
                                still_pending.append(frame)
                        state.pending_outbound = still_pending

                    # Snapshot is authoritative for late joiners; do not accumulate.
                    pending_broadcast = []

                    if self._stop_event.is_set():
                        if shutdown_deadline is None:
                            shutdown_deadline = time.monotonic() + SHUTDOWN_GRACE_SEC
                        if self._all_outbound_drained(viewer_peers):
                            break
                        if time.monotonic() >= shutdown_deadline:
                            break

        for state in viewer_peers.values():
            await self._close_viewer_peer(state)

    async def _handle_signaling_message(
        self,
        *,
        sig_msg: aiohttp.WSMessage,
        ws: aiohttp.ClientWebSocketResponse,
        viewer_peers: dict[str, ViewerPeerState],
    ) -> bool:
        if sig_msg.type == aiohttp.WSMsgType.TEXT:
            raw = str(sig_msg.data).strip()

            if raw.startswith("ERROR|"):
                reason = raw.split("|", 1)[1] if "|" in raw else "UNKNOWN"
                logger.error("Signaling server rejected join for %s: %s", self._room_id, reason)
                return True

            ready_viewer_id = self._parse_ready_viewer_id(raw)
            if ready_viewer_id is not None:
                existing = viewer_peers.pop(ready_viewer_id, None)
                if existing is not None:
                    await self._close_viewer_peer(existing)

                state = await self._create_viewer_peer(ready_viewer_id, ws)
                viewer_peers[ready_viewer_id] = state
                await self._send_offer(state, ws)
                return False

            try:
                envelope = json.loads(raw)
            except json.JSONDecodeError:
                logger.debug("Ignoring non-JSON signaling message: %s", raw)
                return False

            if not isinstance(envelope, dict):
                return False

            msg_type = envelope.get("type")
            payload = envelope.get("payload")

            viewer_id = envelope.get("from")
            if not isinstance(viewer_id, str) or not viewer_id:
                if len(viewer_peers) == 1:
                    viewer_id = next(iter(viewer_peers))
                else:
                    viewer_id = LEGACY_VIEWER_ID

            state = viewer_peers.get(viewer_id)
            if state is None:
                return False

            if msg_type == "answer" and isinstance(payload, dict):
                answer_type = payload.get("type")
                answer_sdp = payload.get("sdp")
                if answer_type == "answer" and isinstance(answer_sdp, str):
                    await state.pc.setRemoteDescription(
                        RTCSessionDescription(sdp=answer_sdp, type=answer_type)
                    )
                    state.remote_description_set = True
                    for candidate in state.pending_remote_ice:
                        try:
                            await state.pc.addIceCandidate(candidate)
                        except Exception:
                            logger.debug(
                                "Ignoring remote ICE add failure for %s/%s",
                                self._room_id,
                                viewer_id,
                            )
                    state.pending_remote_ice.clear()
            elif msg_type == "ice" and isinstance(payload, dict):
                candidate = self._parse_remote_ice_candidate(payload)
                if candidate is not None:
                    if state.remote_description_set:
                        try:
                            await state.pc.addIceCandidate(candidate)
                        except Exception:
                            logger.debug(
                                "Ignoring remote ICE add failure for %s/%s",
                                self._room_id,
                                viewer_id,
                            )
                    else:
                        state.pending_remote_ice.append(candidate)

            return False

        if sig_msg.type in (
            aiohttp.WSMsgType.CLOSE,
            aiohttp.WSMsgType.CLOSED,
            aiohttp.WSMsgType.CLOSING,
            aiohttp.WSMsgType.ERROR,
        ):
            return True

        return False

    async def _create_viewer_peer(
        self,
        viewer_id: str,
        ws: aiohttp.ClientWebSocketResponse,
    ) -> ViewerPeerState:
        pc = RTCPeerConnection(configuration=RTCConfiguration(iceServers=STUN_SERVERS))
        channel = pc.createDataChannel("fernsicht", ordered=True)
        state = ViewerPeerState(viewer_id=viewer_id, pc=pc, channel=channel)

        @channel.on("open")
        def _on_open() -> None:
            logger.debug("DataChannel open for room %s viewer=%s", self._room_id, viewer_id)
            state.channel_open.set()

        @channel.on("close")
        def _on_channel_close() -> None:
            state.channel_open.clear()

        @pc.on("connectionstatechange")
        async def _on_state_change() -> None:
            logger.debug(
                "Peer state for %s viewer=%s: %s",
                self._room_id,
                viewer_id,
                pc.connectionState,
            )
            if pc.connectionState == "disconnected":
                state.disconnected_since = time.monotonic()
            else:
                state.disconnected_since = None

            if pc.connectionState in {"disconnected", "failed", "closed"}:
                state.channel_open.clear()

        @pc.on("icecandidate")
        async def _on_icecandidate(candidate: RTCIceCandidate | None) -> None:
            if candidate is None or ws.closed:
                return
            try:
                await ws.send_str(
                    json.dumps(
                        {
                            "to": viewer_id,
                            "type": "ice",
                            "payload": {
                                "candidate": f"candidate:{candidate_to_sdp(candidate)}",
                                "sdpMid": candidate.sdpMid,
                                "sdpMLineIndex": candidate.sdpMLineIndex,
                            },
                        },
                        separators=(",", ":"),
                    )
                )
            except (ConnectionResetError, RuntimeError):
                return

        return state

    async def _close_viewer_peer(self, state: ViewerPeerState) -> None:
        try:
            if state.channel.readyState != "closed":
                state.channel.close()
        except Exception:
            pass
        await state.pc.close()

    async def _send_offer(
        self,
        state: ViewerPeerState,
        ws: aiohttp.ClientWebSocketResponse,
    ) -> None:
        offer = await state.pc.createOffer()
        await state.pc.setLocalDescription(offer)
        await self._wait_for_ice_gathering_complete(state.pc)

        local = state.pc.localDescription
        if local is None:
            return

        await ws.send_str(
            json.dumps(
                {
                    "to": state.viewer_id,
                    "type": "offer",
                    "payload": {
                        "type": local.type,
                        "sdp": local.sdp,
                    },
                },
                separators=(",", ":"),
            )
        )

    @staticmethod
    async def _wait_for_ice_gathering_complete(pc: RTCPeerConnection) -> None:
        deadline = time.monotonic() + 3.0
        while pc.iceGatheringState != "complete" and time.monotonic() < deadline:
            await asyncio.sleep(0.05)

    def _consume_latest_progress(self) -> str | None:
        with self._lock:
            state = self._latest
            self._latest = None

        return self._progress_frame_from_state(state)

    def _snapshot_progress(self) -> str | None:
        with self._lock:
            state = self._snapshot

        return self._progress_frame_from_state(state)

    def _progress_frame_from_state(self, state: dict[str, Any] | None) -> str | None:
        if state is None:
            return None

        total = state.get("total")
        n = state.get("n")
        if not isinstance(n, int):
            return None

        if total is not None and isinstance(total, int) and total > 0:
            progress = n / total
        else:
            progress = 0.0

        elapsed = state.get("elapsed")
        rate = state.get("rate")
        unit = state.get("unit", "it")

        eta: float | None = None
        if total is not None and rate is not None and rate > 0:
            remaining = max(0, total - n)
            eta = remaining / rate

        return serialize_progress(
            self._task_id,
            progress,
            elapsed=elapsed,
            eta=eta,
            n=n,
            total=total,
            rate=rate,
            unit=unit,
        )

    @staticmethod
    def _try_send_frame(channel: RTCDataChannel, frame: str) -> bool:
        try:
            channel.send(frame)
            return True
        except Exception:
            return False

    @staticmethod
    def _parse_ready_viewer_id(raw: str) -> str | None:
        if raw == "READY":
            return LEGACY_VIEWER_ID
        if raw.startswith("READY|"):
            viewer_id = raw.split("|", 1)[1].strip()
            if viewer_id:
                return viewer_id
        return None

    def _all_outbound_drained(self, viewer_peers: dict[str, ViewerPeerState]) -> bool:
        return all(not state.pending_outbound for state in viewer_peers.values())

    def _build_join_message(self) -> str:
        if self._sender_token:
            return f"JOIN|{self._room_id}|SENDER|{self._sender_token}"
        return f"JOIN|{self._room_id}|SENDER"

    @staticmethod
    def _parse_remote_ice_candidate(payload: dict[str, Any]) -> RTCIceCandidate | None:
        candidate_sdp = payload.get("candidate")
        if not isinstance(candidate_sdp, str) or not candidate_sdp:
            return None
        if candidate_sdp.startswith("candidate:"):
            candidate_sdp = candidate_sdp[len("candidate:") :]

        try:
            candidate = candidate_from_sdp(candidate_sdp)
        except Exception:
            return None

        sdp_mid = payload.get("sdpMid")
        sdp_mline = payload.get("sdpMLineIndex")
        if isinstance(sdp_mid, str):
            candidate.sdpMid = sdp_mid
        if isinstance(sdp_mline, int):
            candidate.sdpMLineIndex = sdp_mline
        return candidate
