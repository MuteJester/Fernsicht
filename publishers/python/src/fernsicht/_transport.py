"""Background WebRTC sender transport for Fernsicht."""

from __future__ import annotations

import asyncio
import json
import logging
import threading
import time
from queue import Empty, Queue
from typing import Any

import aiohttp
from aiortc import RTCConfiguration, RTCDataChannel, RTCIceCandidate, RTCIceServer, RTCPeerConnection, RTCSessionDescription
from aiortc.sdp import candidate_from_sdp

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

STUN_SERVERS = [
    RTCIceServer(urls=["stun:stun.l.google.com:19302"]),
    RTCIceServer(urls=["stun:stun1.l.google.com:19302"]),
]


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
            self._latest = {
                "n": n,
                "total": total,
                "desc": desc,
                "unit": unit,
                "rate": rate,
                "elapsed": elapsed,
            }

    def send_error(self, *, error: str, message: str, fatal: bool) -> None:
        """Record an error and end the current task on the viewer."""
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
        pc = RTCPeerConnection(configuration=RTCConfiguration(iceServers=STUN_SERVERS))
        channel = pc.createDataChannel("fernsicht", ordered=True)

        channel_open = asyncio.Event()

        @channel.on("open")
        def _on_open() -> None:
            logger.debug("DataChannel open for room %s", self._room_id)
            channel_open.set()

        @pc.on("connectionstatechange")
        async def _on_state_change() -> None:
            logger.debug("Peer state for %s: %s", self._room_id, pc.connectionState)

        pending_remote_ice: list[RTCIceCandidate] = []
        remote_description_set = False
        sent_offer = False
        session_frames_sent = False

        pending_outbound: list[str] = []
        last_send = time.monotonic()
        last_tick = time.monotonic()
        shutdown_deadline: float | None = None

        try:
            async with aiohttp.ClientSession() as session:
                async with session.ws_connect(
                    self._signaling_url,
                    heartbeat=30.0,
                    autoping=True,
                    max_msg_size=1_048_576,
                ) as ws:
                    await ws.send_str(self._build_join_message())

                    while True:
                        # Read signaling with short timeout so publish loop continues.
                        try:
                            sig_msg = await ws.receive(timeout=0.2)
                        except asyncio.TimeoutError:
                            sig_msg = None

                        if sig_msg is not None:
                            should_continue = await self._handle_signaling_message(
                                sig_msg=sig_msg,
                                ws=ws,
                                pc=pc,
                                sent_offer=sent_offer,
                                remote_description_set=remote_description_set,
                                pending_remote_ice=pending_remote_ice,
                            )
                            sent_offer = should_continue["sent_offer"]
                            remote_description_set = should_continue["remote_description_set"]
                            if should_continue["stop"]:
                                break

                        # Drain immediate queue.
                        while True:
                            try:
                                pending_outbound.append(self._outbound_immediate.get_nowait())
                            except Empty:
                                break

                        now = time.monotonic()
                        channel_ready_now = (
                            channel_open.is_set() and channel.readyState == "open"
                        )

                        # Send static session frames once DataChannel opens.
                        if channel_ready_now and not session_frames_sent:
                            pending_outbound.append(serialize_identity(self._peer_id))
                            pending_outbound.append(
                                serialize_start(self._task_id, self._desc or "Task")
                            )
                            session_frames_sent = True

                        # Periodic progress / heartbeat.
                        if channel_ready_now and (now - last_tick) >= PUBLISH_INTERVAL:
                            last_tick = now
                            progress = self._consume_latest_progress()
                            if progress is not None:
                                pending_outbound.append(progress)
                            elif (now - last_send) >= HEARTBEAT_INTERVAL:
                                pending_outbound.append(serialize_keepalive())

                        # Attempt to flush outbound frames.
                        if channel_ready_now:
                            still_pending: list[str] = []
                            for frame in pending_outbound:
                                if self._try_send_frame(channel, frame):
                                    last_send = time.monotonic()
                                else:
                                    still_pending.append(frame)
                            pending_outbound = still_pending

                        # Shutdown condition with grace period for final frames.
                        if self._stop_event.is_set():
                            if shutdown_deadline is None:
                                shutdown_deadline = time.monotonic() + SHUTDOWN_GRACE_SEC
                            if not pending_outbound:
                                break
                            if time.monotonic() >= shutdown_deadline:
                                break
        finally:
            await pc.close()

    async def _handle_signaling_message(
        self,
        *,
        sig_msg: aiohttp.WSMessage,
        ws: aiohttp.ClientWebSocketResponse,
        pc: RTCPeerConnection,
        sent_offer: bool,
        remote_description_set: bool,
        pending_remote_ice: list[RTCIceCandidate],
    ) -> dict[str, bool]:
        stop = False

        if sig_msg.type == aiohttp.WSMsgType.TEXT:
            raw = str(sig_msg.data).strip()

            if raw.startswith("ERROR|"):
                reason = raw.split("|", 1)[1] if "|" in raw else "UNKNOWN"
                logger.error("Signaling server rejected join for %s: %s", self._room_id, reason)
                stop = True
                return {
                    "stop": stop,
                    "sent_offer": sent_offer,
                    "remote_description_set": remote_description_set,
                }

            if raw == "READY" and not sent_offer:
                await self._send_offer(pc, ws)
                sent_offer = True
                return {
                    "stop": stop,
                    "sent_offer": sent_offer,
                    "remote_description_set": remote_description_set,
                }

            try:
                envelope = json.loads(raw)
            except json.JSONDecodeError:
                logger.debug("Ignoring non-JSON signaling message: %s", raw)
                return {
                    "stop": stop,
                    "sent_offer": sent_offer,
                    "remote_description_set": remote_description_set,
                }

            msg_type = envelope.get("type")
            payload = envelope.get("payload")

            if msg_type == "answer" and isinstance(payload, dict):
                answer_type = payload.get("type")
                answer_sdp = payload.get("sdp")
                if answer_type == "answer" and isinstance(answer_sdp, str):
                    await pc.setRemoteDescription(
                        RTCSessionDescription(sdp=answer_sdp, type=answer_type)
                    )
                    remote_description_set = True
                    for candidate in pending_remote_ice:
                        await pc.addIceCandidate(candidate)
                    pending_remote_ice.clear()
            elif msg_type == "ice" and isinstance(payload, dict):
                candidate = self._parse_remote_ice_candidate(payload)
                if candidate is not None:
                    if remote_description_set:
                        await pc.addIceCandidate(candidate)
                    else:
                        pending_remote_ice.append(candidate)
        elif sig_msg.type in (
            aiohttp.WSMsgType.CLOSE,
            aiohttp.WSMsgType.CLOSED,
            aiohttp.WSMsgType.CLOSING,
            aiohttp.WSMsgType.ERROR,
        ):
            stop = True

        return {
            "stop": stop,
            "sent_offer": sent_offer,
            "remote_description_set": remote_description_set,
        }

    async def _send_offer(
        self,
        pc: RTCPeerConnection,
        ws: aiohttp.ClientWebSocketResponse,
    ) -> None:
        offer = await pc.createOffer()
        await pc.setLocalDescription(offer)
        await self._wait_for_ice_gathering_complete(pc)

        local = pc.localDescription
        if local is None:
            return

        await ws.send_str(
            json.dumps(
                {
                    "type": "offer",
                    "payload": {
                        "type": local.type,
                        "sdp": local.sdp,
                    },
                }
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

        if state is None:
            return None

        total = state.get("total")
        n = state.get("n")
        if total is None or not isinstance(total, int) or total <= 0:
            return None
        if not isinstance(n, int):
            return None

        progress = n / total
        return serialize_progress(self._task_id, progress)

    @staticmethod
    def _try_send_frame(channel: RTCDataChannel, frame: str) -> bool:
        try:
            channel.send(frame)
            return True
        except Exception:
            return False

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
