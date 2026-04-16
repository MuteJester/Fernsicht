"""Tests for the crypto module."""

from fernsicht._crypto import (
    decrypt,
    decode_payload,
    encode_payload,
    encrypt,
    generate_key,
    generate_topic_id,
    key_from_url,
    key_to_url,
)


def test_generate_key_length():
    key = generate_key()
    assert len(key) == 32


def test_generate_key_is_random():
    k1 = generate_key()
    k2 = generate_key()
    assert k1 != k2


def test_generate_topic_id_format():
    tid = generate_topic_id()
    assert len(tid) == 32
    assert tid == tid.lower()
    # Should be valid hex
    int(tid, 16)


def test_encrypt_decrypt_roundtrip():
    key = generate_key()
    plaintext = b'{"v":1,"n":42,"total":100}'
    encrypted = encrypt(plaintext, key)
    # Encrypted should be larger than plaintext (12 IV + 16 tag + ciphertext)
    assert len(encrypted) > len(plaintext)
    decrypted = decrypt(encrypted, key)
    assert decrypted == plaintext


def test_encrypt_produces_different_output():
    """Each encryption should use a different IV."""
    key = generate_key()
    plaintext = b"same data"
    e1 = encrypt(plaintext, key)
    e2 = encrypt(plaintext, key)
    assert e1 != e2  # Different IVs


def test_encode_decode_payload_roundtrip():
    data = b"\x00\x01\x02\xff" * 10
    encoded = encode_payload(data)
    assert isinstance(encoded, str)
    assert "=" not in encoded  # No padding
    decoded = decode_payload(encoded)
    assert decoded == data


def test_key_url_roundtrip():
    key = generate_key()
    encoded = key_to_url(key)
    assert isinstance(encoded, str)
    assert "=" not in encoded
    recovered = key_from_url(encoded)
    assert recovered == key
