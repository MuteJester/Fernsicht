"""Background WebRTC sender transport for Fernsicht (V2 HTTP polling).

The server is connectionless HTTP. The sender:
  1. polls GET /poll/{room}?secret=X every poll_interval_sec for viewer tickets
  2. for each ticket: setRemoteDescription(viewer_offer), create answer,
     POST /ticket/{id}/answer, then poll GET /ticket/{id}/ice/viewer for ICE
  3. progress frames flow directly P2P over the DataChannel
"""

from __future__ import annotations

import asyncio
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
    serialize_presence,
    serialize_progress,
    serialize_start,
)

logger = logging.getLogger("fernsicht")

PUBLISH_INTERVAL = 0.5
HEARTBEAT_INTERVAL = 5.0
SHUTDOWN_GRACE_SEC = 2.0
ICE_POLL_INTERVAL = 0.5
ICE_POLL_DURATION = 15.0
HTTP_TIMEOUT = aiohttp.ClientTimeout(total=10.0)
DEFAULT_POLL_INTERVAL = 25.0

STUN_SERVERS = [
    RTCIceServer(urls=["stun:stun.l.google.com:19302"]),
    RTCIceServer(urls=["stun:stun1.l.google.com:19302"]),
]


@dataclass(slots=True)
class ViewerPeerState:
    ticket_id: str
    pc: RTCPeerConnection
    channel: RTCDataChannel
    channel_open: asyncio.Event = field(default_factory=asyncio.Event)
    session_frames_sent: bool = False
    pending_outbound: list[str] = field(default_factory=list)
    last_send: float = field(default_factory=time.monotonic)
    disconnected_since: float | None = None
    ice_recv_seq: int = 0
    ice_poll_task: asyncio.Task | None = None
    pending_sender_ice: list[dict[str, Any]] = field(default_factory=list)
    # Human-friendly name from the viewer's HELLO frame. Unset until the
    # viewer identifies itself; such viewers are omitted from presence.
    name: str | None = None


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
        base_url: str,
        sender_secret: str,
        poll_interval_sec: float = DEFAULT_POLL_INTERVAL,
    ) -> None:
        self._room_id = room_id
        self._task_id = room_id[:8]
        self._peer_id = f"py-{room_id[:8]}"
        self._base_url = base_url.rstrip("/")
        self._sender_secret = sender_secret
        self._poll_interval = max(1.0, float(poll_interval_sec))
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

        # Presence: set when a viewer identifies itself (HELLO) or a named
        # peer is culled. Consumed by the main loop to broadcast a fresh V|
        # frame to all viewers. Only touched from the asyncio event loop.
        self._presence_dirty = False

        self._thread = threading.Thread(
            target=self._run,
            name=f"fernsicht-{room_id[:8]}",
            daemon=True,
        )
        self._thread.start()

    # -- User-thread API -----------------------------------------------------

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
        if self._closed:
            return
        self._closed = True
        self._outbound_immediate.put(serialize_end(self._task_id))
        self._stop_event.set()
        self._thread.join(timeout=5.0)

    # -- Background thread entry --------------------------------------------

    def _run(self) -> None:
        try:
            asyncio.run(self._run_async())
        except Exception:
            logger.exception("WebRTC transport thread crashed")

    async def _run_async(self) -> None:
        viewer_peers: dict[str, ViewerPeerState] = {}
        pending_broadcast: list[str] = []
        last_progress_tick = time.monotonic()
        last_poll_tick = 0.0
        shutdown_deadline: float | None = None

        async with aiohttp.ClientSession(timeout=HTTP_TIMEOUT) as http:
            while True:
                now = time.monotonic()

                # 1) Poll for new viewer tickets
                if (now - last_poll_tick) >= self._poll_interval:
                    last_poll_tick = now
                    try:
                        tickets = await self._poll_tickets(http)
                    except Exception as exc:
                        logger.debug("Poll failed: %s", exc)
                        tickets = []
                    for ticket in tickets:
                        ticket_id = ticket.get("ticket_id")
                        offer = ticket.get("offer")
                        if not isinstance(ticket_id, str) or not isinstance(offer, dict):
                            continue
                        if ticket_id in viewer_peers:
                            continue
                        try:
                            state = await self._handle_new_ticket(http, ticket_id, offer)
                            if state is not None:
                                viewer_peers[ticket_id] = state
                        except Exception:
                            logger.exception("Failed to handle ticket %s", ticket_id)

                # 2) Drain immediate outbound queue
                while True:
                    try:
                        pending_broadcast.append(self._outbound_immediate.get_nowait())
                    except Empty:
                        break

                # 3) Snapshot latest progress for broadcast
                if (now - last_progress_tick) >= PUBLISH_INTERVAL:
                    last_progress_tick = now
                    progress = self._consume_latest_progress()
                    if progress is not None:
                        pending_broadcast.append(progress)

                # 4) Cull stale viewer peers
                stale = [
                    tid
                    for tid, state in viewer_peers.items()
                    if (
                        state.pc.connectionState in {"failed", "closed"}
                        or (
                            state.pc.connectionState == "disconnected"
                            and state.disconnected_since is not None
                            and (now - state.disconnected_since) >= 20.0
                        )
                    )
                ]
                for tid in stale:
                    culled = viewer_peers.pop(tid)
                    if culled.name is not None:
                        self._presence_dirty = True
                    await self._close_viewer_peer(culled)

                # 4a) If presence changed (HELLO arrived or a named peer was
                # culled), queue a fresh V| frame for all viewers.
                if self._presence_dirty:
                    self._presence_dirty = False
                    names = [p.name for p in viewer_peers.values() if p.name]
                    pending_broadcast.append(serialize_presence(names))

                # 5) Send pending frames on each open DataChannel
                for state in viewer_peers.values():
                    if not (
                        state.channel_open.is_set()
                        and state.channel.readyState == "open"
                    ):
                        continue

                    if not state.session_frames_sent:
                        state.pending_outbound.append(
                            serialize_identity(self._peer_id)
                        )
                        state.pending_outbound.append(
                            serialize_start(self._task_id, self._desc or "Task")
                        )
                        snap = self._snapshot_progress()
                        if snap is not None:
                            state.pending_outbound.append(snap)
                        # Catch new viewers up on who's currently watching.
                        names = [p.name for p in viewer_peers.values() if p.name]
                        if names:
                            state.pending_outbound.append(serialize_presence(names))
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

                # Snapshot is authoritative for late joiners.
                pending_broadcast = []

                # 6) Shutdown handling
                if self._stop_event.is_set():
                    if shutdown_deadline is None:
                        shutdown_deadline = time.monotonic() + SHUTDOWN_GRACE_SEC
                    if self._all_outbound_drained(viewer_peers):
                        break
                    if time.monotonic() >= shutdown_deadline:
                        break

                await asyncio.sleep(0.1)

        # Tear down all peers
        for state in viewer_peers.values():
            if state.ice_poll_task:
                state.ice_poll_task.cancel()
            await self._close_viewer_peer(state)

    # -- HTTP signaling helpers ---------------------------------------------

    async def _poll_tickets(self, http: aiohttp.ClientSession) -> list[dict[str, Any]]:
        url = f"{self._base_url}/poll/{self._room_id}"
        # Use Authorization: Bearer so the sender secret never appears in URLs
        # (and therefore never in proxy access logs). The server still accepts
        # ?secret=... as a deprecated fallback for older clients.
        headers = {"Authorization": f"Bearer {self._sender_secret}"}
        async with http.get(url, headers=headers) as resp:
            if resp.status == 404:
                logger.warning("Room not found on server: %s", self._room_id)
                return []
            if resp.status == 403:
                logger.error("Invalid sender secret for room %s", self._room_id)
                return []
            if resp.status >= 400:
                logger.debug("Poll returned HTTP %d", resp.status)
                return []
            data = await resp.json()
            tickets = data.get("tickets", [])
            return tickets if isinstance(tickets, list) else []

    async def _handle_new_ticket(
        self,
        http: aiohttp.ClientSession,
        ticket_id: str,
        offer: dict[str, Any],
    ) -> ViewerPeerState | None:
        offer_type = offer.get("type")
        offer_sdp = offer.get("sdp")
        if offer_type != "offer" or not isinstance(offer_sdp, str):
            return None

        pc = RTCPeerConnection(
            configuration=RTCConfiguration(iceServers=STUN_SERVERS)
        )

        # Wait for the viewer's DataChannel to arrive.
        channel_holder: dict[str, RTCDataChannel] = {}

        @pc.on("datachannel")
        def _on_datachannel(channel: RTCDataChannel) -> None:
            channel_holder["channel"] = channel

        @pc.on("icecandidate")
        async def _on_icecandidate(candidate: RTCIceCandidate | None) -> None:
            if candidate is None:
                return
            payload = {
                "candidate": f"candidate:{candidate_to_sdp(candidate)}",
                "sdpMid": candidate.sdpMid,
                "sdpMLineIndex": candidate.sdpMLineIndex,
            }
            # Store for batched send; actual POST happens after we post the answer.
            pending = state_ref.get("state")
            if pending is not None:
                pending.pending_sender_ice.append(payload)

        state_ref: dict[str, ViewerPeerState | None] = {"state": None}

        # Set remote description from viewer's offer
        await pc.setRemoteDescription(
            RTCSessionDescription(sdp=offer_sdp, type=offer_type)
        )

        # Create answer; aiortc gathers ICE during setLocalDescription
        answer = await pc.createAnswer()
        await pc.setLocalDescription(answer)
        await self._wait_for_ice_gathering_complete(pc)

        local = pc.localDescription
        if local is None:
            await pc.close()
            return None

        # POST answer to server
        if not await self._post_answer(http, ticket_id, local.type, local.sdp):
            await pc.close()
            return None

        # Find or create the DataChannel object. Viewer creates it, so it arrives
        # via the datachannel event. If it hasn't arrived yet, create a placeholder
        # and attach to it when it arrives.
        channel = channel_holder.get("channel")
        if channel is None:
            # Should arrive during ICE negotiation; set up a proxy that updates later
            logger.debug("DataChannel not yet received for ticket %s", ticket_id)
            # We'll patch up in the datachannel event below
            # For simplicity, wait briefly for it.
            for _ in range(20):
                await asyncio.sleep(0.05)
                channel = channel_holder.get("channel")
                if channel is not None:
                    break

        if channel is None:
            logger.warning("DataChannel never opened for ticket %s", ticket_id)
            await pc.close()
            return None

        state = ViewerPeerState(ticket_id=ticket_id, pc=pc, channel=channel)
        state_ref["state"] = state

        @channel.on("open")
        def _on_open() -> None:
            state.channel_open.set()

        @channel.on("close")
        def _on_channel_close() -> None:
            state.channel_open.clear()

        @channel.on("message")
        def _on_channel_message(msg: Any) -> None:
            # Viewer → sender frames. Only HELLO is meaningful today;
            # keepalives ("K") and unknown tags are ignored.
            if not isinstance(msg, str):
                return
            if msg.startswith("HELLO|"):
                raw = msg[len("HELLO|") :].strip()
                name = raw.replace("|", "")[:32]
                if name and name != state.name:
                    state.name = name
                    self._presence_dirty = True

        if channel.readyState == "open":
            state.channel_open.set()

        @pc.on("connectionstatechange")
        async def _on_state_change() -> None:
            if pc.connectionState == "disconnected":
                state.disconnected_since = time.monotonic()
            else:
                state.disconnected_since = None
            if pc.connectionState in {"disconnected", "failed", "closed"}:
                state.channel_open.clear()

        # Post any ICE that was gathered before we had a state ref
        await self._flush_sender_ice(http, ticket_id, state)

        # Start polling for viewer's trickle ICE
        state.ice_poll_task = asyncio.create_task(
            self._poll_viewer_ice(http, ticket_id, state, pc)
        )

        return state

    async def _post_answer(
        self,
        http: aiohttp.ClientSession,
        ticket_id: str,
        answer_type: str,
        answer_sdp: str,
    ) -> bool:
        url = f"{self._base_url}/ticket/{ticket_id}/answer"
        body = {
            "answer": {"type": answer_type, "sdp": answer_sdp},
            "secret": self._sender_secret,
        }
        try:
            async with http.post(url, json=body) as resp:
                if resp.status != 200:
                    logger.warning(
                        "POST /ticket/%s/answer returned %d", ticket_id, resp.status
                    )
                    return False
                return True
        except Exception as exc:
            logger.debug("POST answer failed for %s: %s", ticket_id, exc)
            return False

    async def _flush_sender_ice(
        self,
        http: aiohttp.ClientSession,
        ticket_id: str,
        state: ViewerPeerState,
    ) -> None:
        if not state.pending_sender_ice:
            return
        batch = list(state.pending_sender_ice)
        state.pending_sender_ice.clear()
        url = f"{self._base_url}/ticket/{ticket_id}/ice/sender"
        body = {"candidates": batch, "secret": self._sender_secret}
        try:
            async with http.post(url, json=body) as resp:
                if resp.status != 200:
                    logger.debug(
                        "POST sender ICE returned %d for %s", resp.status, ticket_id
                    )
        except Exception as exc:
            logger.debug("POST sender ICE failed for %s: %s", ticket_id, exc)

    async def _poll_viewer_ice(
        self,
        http: aiohttp.ClientSession,
        ticket_id: str,
        state: ViewerPeerState,
        pc: RTCPeerConnection,
    ) -> None:
        deadline = time.monotonic() + ICE_POLL_DURATION
        url = f"{self._base_url}/ticket/{ticket_id}/ice/viewer"

        while time.monotonic() < deadline and not self._stop_event.is_set():
            try:
                params = {"since": str(state.ice_recv_seq)}
                async with http.get(url, params=params) as resp:
                    if resp.status == 200:
                        data = await resp.json()
                        candidates = data.get("candidates", [])
                        seq = data.get("seq", state.ice_recv_seq)
                        if isinstance(seq, int):
                            state.ice_recv_seq = seq
                        if isinstance(candidates, list):
                            for c in candidates:
                                if isinstance(c, dict):
                                    cand = self._parse_remote_ice_candidate(c)
                                    if cand is not None:
                                        try:
                                            await pc.addIceCandidate(cand)
                                        except Exception:
                                            logger.debug(
                                                "Failed to add ICE for %s", ticket_id
                                            )
                    elif resp.status == 404:
                        return
            except Exception as exc:
                logger.debug("Viewer ICE poll failed for %s: %s", ticket_id, exc)

            # Stop polling once the connection is established
            if pc.connectionState in {"connected", "completed"}:
                return

            await asyncio.sleep(ICE_POLL_INTERVAL)

    # -- Peer cleanup --------------------------------------------------------

    async def _close_viewer_peer(self, state: ViewerPeerState) -> None:
        if state.ice_poll_task and not state.ice_poll_task.done():
            state.ice_poll_task.cancel()
        try:
            if state.channel.readyState != "closed":
                state.channel.close()
        except Exception:
            pass
        try:
            await state.pc.close()
        except Exception:
            pass

    @staticmethod
    async def _wait_for_ice_gathering_complete(pc: RTCPeerConnection) -> None:
        deadline = time.monotonic() + 3.0
        while pc.iceGatheringState != "complete" and time.monotonic() < deadline:
            await asyncio.sleep(0.05)

    # -- Progress frame helpers ---------------------------------------------

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
    def _all_outbound_drained(viewer_peers: dict[str, ViewerPeerState]) -> bool:
        return all(not state.pending_outbound for state in viewer_peers.values())

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
