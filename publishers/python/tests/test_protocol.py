"""Tests for the protocol module."""

import json

from fernsicht._protocol import (
    build_topic,
    serialize_error,
    serialize_heartbeat,
    serialize_session,
    serialize_state,
)


# --- build_topic ---

def test_build_topic():
    assert build_topic("abc123") == "fernsicht/runs/abc123"


# --- serialize_state (progress) ---

def test_serialize_state_has_type():
    data = serialize_state(n=50, total=100, elapsed=2.5)
    msg = json.loads(data)
    assert msg["type"] == "progress"


def test_serialize_state_required_fields():
    data = serialize_state(n=50, total=100, elapsed=2.5)
    msg = json.loads(data)
    assert msg["v"] == 1
    assert msg["n"] == 50
    assert msg["total"] == 100
    assert msg["elapsed"] == 2.5
    assert "ts" in msg
    assert "done" not in msg


def test_serialize_state_with_optional_fields():
    data = serialize_state(
        n=10, total=None, desc="test", unit="files",
        rate=5.25, elapsed=1.0,
    )
    msg = json.loads(data)
    assert msg["total"] is None
    assert msg["desc"] == "test"
    assert msg["unit"] == "files"
    assert msg["rate"] == 5.25


def test_serialize_state_done():
    data = serialize_state(n=100, total=100, elapsed=10.0, done=True)
    msg = json.loads(data)
    assert msg["done"] is True


def test_serialize_state_default_unit_omitted():
    data = serialize_state(n=0, total=10, elapsed=0.0)
    msg = json.loads(data)
    assert "unit" not in msg


def test_serialize_state_compact_json():
    data = serialize_state(n=1, total=2, elapsed=0.1)
    text = data.decode("utf-8")
    assert " " not in text


# --- serialize_heartbeat ---

def test_heartbeat_type():
    data = serialize_heartbeat(elapsed=5.0)
    msg = json.loads(data)
    assert msg["type"] == "heartbeat"
    assert msg["v"] == 1
    assert "ts" in msg


def test_heartbeat_elapsed():
    data = serialize_heartbeat(elapsed=12.345)
    msg = json.loads(data)
    assert msg["elapsed"] == 12.345


def test_heartbeat_minimal_fields():
    data = serialize_heartbeat(elapsed=1.0)
    msg = json.loads(data)
    # Should only have type, v, ts, elapsed
    assert set(msg.keys()) == {"type", "v", "ts", "elapsed"}


# --- serialize_error ---

def test_error_type():
    data = serialize_error(error="ValueError", message="bad value", fatal=True)
    msg = json.loads(data)
    assert msg["type"] == "error"
    assert msg["v"] == 1


def test_error_fields():
    data = serialize_error(error="RuntimeError", message="something broke", fatal=False)
    msg = json.loads(data)
    assert msg["error"] == "RuntimeError"
    assert msg["message"] == "something broke"
    assert msg["fatal"] is False


def test_error_message_truncation():
    long_msg = "x" * 1000
    data = serialize_error(error="Error", message=long_msg, fatal=True)
    msg = json.loads(data)
    assert len(msg["message"]) == 512


# --- serialize_session ---

def test_session_type():
    data = serialize_session(total=100, pub_version="0.1.0")
    msg = json.loads(data)
    assert msg["type"] == "session"
    assert msg["v"] == 1


def test_session_fields():
    data = serialize_session(
        desc="Training", total=1000, unit="samples",
        pub_lang="python", pub_version="0.2.0",
    )
    msg = json.loads(data)
    assert msg["desc"] == "Training"
    assert msg["total"] == 1000
    assert msg["unit"] == "samples"
    assert msg["pub"] == {"lang": "python", "pkg_version": "0.2.0"}


def test_session_default_unit_omitted():
    data = serialize_session(total=10, pub_version="0.1.0")
    msg = json.loads(data)
    assert "unit" not in msg


def test_session_null_total():
    data = serialize_session(total=None, pub_version="0.1.0")
    msg = json.loads(data)
    assert msg["total"] is None


def test_session_no_desc():
    data = serialize_session(total=10, pub_version="0.1.0")
    msg = json.loads(data)
    assert "desc" not in msg
