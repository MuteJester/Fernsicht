"""Tests for the pipe-delimited wire protocol."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from fernsicht._wire import (
    serialize_end,
    serialize_identity,
    serialize_keepalive,
    serialize_presence,
    serialize_progress,
    serialize_start,
)


def test_identity_serialization() -> None:
    assert serialize_identity("peer-abc") == "ID|peer-abc"


def test_identity_rejects_empty() -> None:
    with pytest.raises(ValueError, match="peer_id"):
        serialize_identity("")


def test_start_serialization() -> None:
    assert serialize_start("t1", "Training") == "START|t1|Training"


def test_start_allows_pipe_in_label() -> None:
    # Labels should not contain '|', but serializer doesn't validate — parser splits max N times.
    # Document that labels with '|' will be truncated by the parser.
    result = serialize_start("t1", "hello|world")
    assert result.startswith("START|t1|")


def test_start_rejects_empty_task_id() -> None:
    with pytest.raises(ValueError, match="task_id"):
        serialize_start("", "label")


def test_start_rejects_empty_label() -> None:
    with pytest.raises(ValueError, match="label"):
        serialize_start("t1", "")


def test_end_serialization() -> None:
    assert serialize_end("t1") == "END|t1"


def test_end_rejects_empty_task_id() -> None:
    with pytest.raises(ValueError, match="task_id"):
        serialize_end("")


def test_keepalive() -> None:
    assert serialize_keepalive() == "K"


# --- Progress message (the rich 9-field format) -------------------------------


def test_progress_with_all_stats() -> None:
    out = serialize_progress(
        "t1",
        0.4523,
        elapsed=12.3,
        eta=15.0,
        n=452,
        total=1000,
        rate=36.7,
        unit="it",
    )
    assert out == "P|t1|0.4523|12.3|15.0|452|1000|36.70|it"


def test_progress_has_9_fields() -> None:
    """The frontend parser expects exactly 9 pipe-delimited fields."""
    out = serialize_progress(
        "t1", 0.5, elapsed=1.0, eta=1.0, n=1, total=2, rate=0.5, unit="it"
    )
    assert len(out.split("|")) == 9


def test_progress_with_none_fields_uses_dash() -> None:
    out = serialize_progress("t1", 0.5)
    # tag | task_id | value | elapsed | eta | n | total | rate | unit
    assert out == "P|t1|0.5000|-|-|-|-|-|it"


def test_progress_clamps_value_above_one() -> None:
    out = serialize_progress("t1", 1.5)
    assert out.split("|")[2] == "1.0000"


def test_progress_clamps_negative_value() -> None:
    out = serialize_progress("t1", -0.1)
    assert out.split("|")[2] == "0.0000"


def test_progress_value_has_4_decimals() -> None:
    out = serialize_progress("t1", 1 / 3)
    assert out.split("|")[2] == "0.3333"


def test_progress_elapsed_and_eta_have_1_decimal() -> None:
    out = serialize_progress("t1", 0.5, elapsed=1.2345, eta=9.8765)
    parts = out.split("|")
    assert parts[3] == "1.2"
    assert parts[4] == "9.9"


def test_progress_rate_has_2_decimals() -> None:
    out = serialize_progress("t1", 0.5, rate=36.789)
    assert out.split("|")[7] == "36.79"


def test_progress_integer_fields_serialize_without_decimals() -> None:
    out = serialize_progress("t1", 0.5, n=100, total=500)
    parts = out.split("|")
    assert parts[5] == "100"
    assert parts[6] == "500"


def test_progress_custom_unit() -> None:
    out = serialize_progress("t1", 0.5, unit="epochs")
    assert out.split("|")[8] == "epochs"


def test_progress_rejects_empty_task_id() -> None:
    with pytest.raises(ValueError, match="task_id"):
        serialize_progress("", 0.5)


def test_progress_handles_mixed_none() -> None:
    """Partial stats: total known but rate is None."""
    out = serialize_progress("t1", 0.5, elapsed=10.0, n=50, total=100)
    parts = out.split("|")
    assert parts[3] == "10.0"  # elapsed
    assert parts[4] == "-"  # eta
    assert parts[5] == "50"  # n
    assert parts[6] == "100"  # total
    assert parts[7] == "-"  # rate
    assert parts[8] == "it"  # unit


# --- Cross-layer contract with the frontend parser ---------------------------


# --- Presence message --------------------------------------------------------


def test_presence_empty() -> None:
    assert serialize_presence([]) == "V"


def test_presence_single_viewer() -> None:
    assert serialize_presence(["orion"]) == "V|orion"


def test_presence_multiple_viewers() -> None:
    assert serialize_presence(["orion", "vega", "lyra"]) == "V|orion|vega|lyra"


def test_presence_strips_pipes_from_names() -> None:
    # Pipes in names would corrupt the wire format; serializer must strip.
    assert serialize_presence(["or|ion", "v|ega"]) == "V|orion|vega"


def test_presence_truncates_long_names() -> None:
    long = "a" * 64
    parts = serialize_presence([long]).split("|")
    assert len(parts[1]) == 32


def test_presence_drops_empty_names() -> None:
    assert serialize_presence(["", "orion", "   ", "vega"]) == "V|orion|vega"


def test_presence_trims_whitespace() -> None:
    assert serialize_presence(["  orion  "]) == "V|orion"


# --- Cross-layer contract with the frontend parser ---------------------------


# --- Cross-implementation wire corpus ----------------------------------------
#
# The Go bridge (bridge/internal/wire) and this SDK MUST produce
# byte-identical wire frames for the same inputs. The shared corpus at
# bridge/internal/wire/testdata/corpus.json is loaded by both
# bridge/internal/wire/wire_test.go and this file. A change to either
# serializer that breaks the corpus fails CI on both sides — that's the
# whole point. If you add a vector, add it once; both implementations
# pick it up.


_CORPUS_PATH = (
    Path(__file__).resolve().parents[3]
    / "bridge"
    / "internal"
    / "wire"
    / "testdata"
    / "corpus.json"
)


def _load_corpus() -> list[dict[str, Any]]:
    if not _CORPUS_PATH.exists():
        # Skip cleanly when the publisher is consumed standalone (e.g.
        # someone vendored just publishers/python/ without the bridge
        # alongside). The corpus is the contract; if it's missing, the
        # Python SDK still works — it just can't enforce the bridge
        # contract from here.
        return []
    return json.loads(_CORPUS_PATH.read_text())["vectors"]


_CORPUS_VECTORS = _load_corpus()


def _dispatch(vec: dict[str, Any]) -> str:
    fn = vec["fn"]
    args = vec["args"]
    if fn == "identity":
        return serialize_identity(args["peer_id"])
    if fn == "start":
        return serialize_start(args["task_id"], args["label"])
    if fn == "end":
        return serialize_end(args["task_id"])
    if fn == "keepalive":
        return serialize_keepalive()
    if fn == "presence":
        return serialize_presence(args["viewers"])
    if fn == "progress":
        return serialize_progress(
            args["task_id"],
            args["value"],
            elapsed=args.get("elapsed"),
            eta=args.get("eta"),
            n=args.get("n"),
            total=args.get("total"),
            rate=args.get("rate"),
            unit=args.get("unit", "it"),
        )
    raise ValueError(f"unknown corpus fn: {fn!r}")


@pytest.mark.skipif(
    not _CORPUS_VECTORS,
    reason=f"wire corpus not found at {_CORPUS_PATH}; run from full Fernsicht checkout",
)
@pytest.mark.parametrize("vec", _CORPUS_VECTORS, ids=[v["name"] for v in _CORPUS_VECTORS])
def test_corpus_vector(vec: dict[str, Any]) -> None:
    """Each vector must serialize to its documented `expected` byte sequence."""
    got = _dispatch(vec)
    assert got == vec["expected"], (
        f"vector {vec['name']!r}: expected {vec['expected']!r}, got {got!r}"
    )


def test_corpus_has_at_least_ten_vectors() -> None:
    """Plan §14 Phase 1 requires 10+ vectors. Guard against shrinkage."""
    assert len(_CORPUS_VECTORS) >= 10, (
        f"corpus shrunk below 10 vectors (found {len(_CORPUS_VECTORS)})"
    )


# --- Direct contract test (kept for in-file completeness) --------------------


def test_field_order_matches_frontend_parser() -> None:
    """
    The frontend TypeScript parser at frontend/src/protocol.ts expects:
      parts[0] = "P"
      parts[1] = taskId
      parts[2] = value (float)
      parts[3] = elapsed
      parts[4] = eta
      parts[5] = n
      parts[6] = total
      parts[7] = rate
      parts[8] = unit

    If this test fails, the viewer will silently misparse all progress updates.
    """
    out = serialize_progress(
        task_id="task-1",
        value=0.75,
        elapsed=30.0,
        eta=10.0,
        n=750,
        total=1000,
        rate=25.0,
        unit="files",
    )
    parts = out.split("|")
    assert parts[0] == "P"
    assert parts[1] == "task-1"
    assert float(parts[2]) == 0.75
    assert float(parts[3]) == 30.0
    assert float(parts[4]) == 10.0
    assert int(parts[5]) == 750
    assert int(parts[6]) == 1000
    assert float(parts[7]) == 25.0
    assert parts[8] == "files"
