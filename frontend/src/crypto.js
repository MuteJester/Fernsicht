/** AES-256-GCM decryption using the Web Crypto API. */
const IV_SIZE = 12;
/** Decode a base64url string (no padding) to Uint8Array. */
export function base64urlDecode(encoded) {
    // Restore base64 from base64url
    let b64 = encoded.replace(/-/g, "+").replace(/_/g, "/");
    // Add padding
    while (b64.length % 4 !== 0) {
        b64 += "=";
    }
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
}
/** Import a raw AES-256 key for GCM decryption. */
export async function importKey(rawKey) {
    return crypto.subtle.importKey("raw", rawKey.buffer, { name: "AES-GCM" }, false, ["decrypt"]);
}
/** Decrypt a wire payload: base64url(IV || ciphertext || tag). */
export async function decryptPayload(wireBase64url, key) {
    const raw = base64urlDecode(wireBase64url);
    const iv = raw.slice(0, IV_SIZE);
    const ciphertextAndTag = raw.slice(IV_SIZE);
    const plaintext = await crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertextAndTag);
    return new TextDecoder().decode(plaintext);
}
