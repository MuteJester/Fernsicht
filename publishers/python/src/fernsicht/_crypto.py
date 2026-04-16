"""AES-256-GCM encryption and key management for Fernsicht."""

from __future__ import annotations

import base64
import os
import uuid

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

IV_SIZE = 12
KEY_SIZE = 32


def generate_key() -> bytes:
    """Generate a 256-bit AES key from a CSPRNG."""
    return os.urandom(KEY_SIZE)


def generate_topic_id() -> str:
    """Generate a UUID4 topic ID as lowercase hex without hyphens."""
    return uuid.uuid4().hex


def encrypt(plaintext: bytes, key: bytes) -> bytes:
    """Encrypt plaintext with AES-256-GCM.

    Returns: iv (12 bytes) || ciphertext || auth_tag (16 bytes)
    """
    iv = os.urandom(IV_SIZE)
    aesgcm = AESGCM(key)
    # AESGCM.encrypt appends the 16-byte auth tag to the ciphertext
    ciphertext_and_tag = aesgcm.encrypt(iv, plaintext, None)
    return iv + ciphertext_and_tag


def decrypt(payload: bytes, key: bytes) -> bytes:
    """Decrypt an AES-256-GCM payload (iv || ciphertext || tag)."""
    iv = payload[:IV_SIZE]
    ciphertext_and_tag = payload[IV_SIZE:]
    aesgcm = AESGCM(key)
    return aesgcm.decrypt(iv, ciphertext_and_tag, None)


def encode_payload(payload: bytes) -> str:
    """Base64url encode without padding (for MQTT wire format)."""
    return base64.urlsafe_b64encode(payload).rstrip(b"=").decode("ascii")


def decode_payload(encoded: str) -> bytes:
    """Decode a base64url string without padding."""
    # Add back padding
    padded = encoded + "=" * (-len(encoded) % 4)
    return base64.urlsafe_b64decode(padded)


def key_to_url(key: bytes) -> str:
    """Encode an AES key as base64url without padding for URL fragment."""
    return base64.urlsafe_b64encode(key).rstrip(b"=").decode("ascii")


def key_from_url(encoded: str) -> bytes:
    """Decode a base64url key from a URL fragment."""
    return decode_payload(encoded)
