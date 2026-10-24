/* End-to-end encryption for one terminal socket at a time between a window
 * reached through the relay and this desktop -- so that the relay carrying
 * it, shared or run by a customer alike, can never read what crosses it.
 *
 * This is a byte-for-byte port, in WebCrypto, of flockdeck-relay's
 * internal/e2e package (Go), which is this scheme's reference implementation
 * and carries the full account of why it is shaped this way. It is also,
 * script for script, the same port flockdeck-remote's own web/app/e2e.js
 * already is (that file's own doc comment is the fuller version of what
 * follows) -- kept as a classic script and a plain namespace, FlockdeckE2E,
 * rather than an ES module: this front end is one, so this file loads the
 * same way vendor/xterm.js does, ahead of app.js.
 *
 * # Keys
 *
 * This window holds one long-term P-256 key pair, generated once with
 * getIdentity and never again: the private half is a non-extractable
 * WebCrypto key, kept in IndexedDB, that nothing which can read storage --
 * a compromised page's own script included -- can ever read out, only ask
 * WebCrypto to use. The public half is registered with the relay
 * (POST /.flockdeck-e2e-key, see ensureRegistered) once this window is
 * reached through it -- deskorigin.go's own endpoint for exactly this,
 * answered by the relay itself rather than proxied to the desktop, so a
 * compromised desktop can never name some other device's key to overwrite.
 * A window opened directly, on the desktop itself, has no relay to register
 * with and no reason to: see app.js's own use of remoteWindow.
 *
 * That registered key is a second one for this same device, apart from
 * whatever it may also hold for flockdeck-remote's own origin -- see
 * store.go's own doc on Device.DeskPublicKey for why one IndexedDB, scoped
 * to one origin, cannot answer for both.
 *
 * # Handshake, fingerprint and frames
 *
 * Identical to flockdeck-remote's own copy of this file: a fresh
 * StartDeviceHandshake-style exchange per terminal socket, deriving two
 * AES-256-GCM keys (one per direction) from three ECDH terms for forward
 * secrecy and mutual authentication, and a Fingerprint a person can compare
 * on two screens for what the handshake's own authentication cannot close by
 * itself -- a relay that swapped either side's registered key the moment it
 * was handed out. See flockdeck-remote's web/app/e2e.js for the long
 * version; nothing about the scheme itself differs here.
 */

(() => {
  "use strict";

  /** VERSION is the wire version of every handshake message and frame this
   *  module makes, matching internal/e2e's Version. */
  const VERSION = 1;

  /** The two roles a Session can hold, matching internal/e2e's Role. This
   *  window is always ROLE_DEVICE; ROLE_HOST exists so respondHostHandshake
   *  (below) can play the desktop's side of the handshake in tests, checked
   *  against fixed vectors from the Go package itself. */
  const ROLE_DEVICE = 0;
  const ROLE_HOST = 1;

  const CURVE = "P-256";
  const ECDH_ALG = { name: "ECDH", namedCurve: CURVE };
  const KEY_LEN_BITS = 256; // AES-256
  const NONCE_PREFIX_LEN = 4;
  const COUNTER_LEN = 8;

  const INFO_DEVICE_TO_HOST_KEY = "flockdeck terminal e2e v1 device->host key";
  const INFO_HOST_TO_DEVICE_KEY = "flockdeck terminal e2e v1 host->device key";
  const INFO_DEVICE_TO_HOST_NONCE = "flockdeck terminal e2e v1 device->host nonce";
  const INFO_HOST_TO_DEVICE_NONCE = "flockdeck terminal e2e v1 host->device nonce";

  // ----------------------------------------------------------- encoding

  function toBase64Url(bytes) {
    let bin = "";
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function fromBase64Url(s) {
    let b64 = String(s).replace(/-/g, "+").replace(/_/g, "/");
    while (b64.length % 4) b64 += "=";
    let bin;
    try {
      bin = atob(b64);
    } catch (err) {
      throw new Error("e2e: public key is not base64url: " + err.message);
    }
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  /** encodePublicKey is how a public key is given to the relay to store, and
   *  how the hello message gives one back: unpadded base64url of the
   *  uncompressed P-256 point (65 bytes), matching internal/e2e's
   *  EncodePublicKey. rawBytes is what exportRawPublicKey (or a
   *  CryptoKeyPair's own publicKeyRaw) gives. */
  function encodePublicKey(rawBytes) {
    return toBase64Url(rawBytes instanceof Uint8Array ? rawBytes : new Uint8Array(rawBytes));
  }

  async function importPublicKeyRaw(rawBytes) {
    try {
      return await crypto.subtle.importKey("raw", rawBytes, ECDH_ALG, true, []);
    } catch (err) {
      throw new Error("e2e: public key is not a P-256 point: " + err.message);
    }
  }

  /** decodePublicKey reads a public key as encodePublicKey writes it, matching
   *  internal/e2e's DecodePublicKey: refusing anything that is not base64url
   *  or not a point on the curve. */
  async function decodePublicKey(s) {
    return importPublicKeyRaw(fromBase64Url(s));
  }

  /** decodePublicKeyRaw reads a public key as encodePublicKey writes it, as
   *  fingerprint takes it: the raw point, not imported into a CryptoKey (a
   *  fingerprint is a hash of the bytes, not an ECDH operand, so there is
   *  nothing for WebCrypto to do with it here). */
  function decodePublicKeyRaw(s) {
    return fromBase64Url(s);
  }

  async function exportRawPublicKey(key) {
    return new Uint8Array(await crypto.subtle.exportKey("raw", key));
  }

  function concatBytes(...arrays) {
    const total = arrays.reduce((n, a) => n + a.length, 0);
    const out = new Uint8Array(total);
    let at = 0;
    for (const a of arrays) { out.set(a, at); at += a.length; }
    return out;
  }

  const FINGERPRINT_GROUPS = 6;
  const FINGERPRINT_GROUP_BYTES = 5;
  const FINGERPRINT_GROUP_MOD = 100000n;

  /** fingerprint reduces a device's and a host's long-term public keys, raw
   *  (as encodePublicKey takes them, not CryptoKey objects), to a short code
   *  a person can compare on two screens -- matching internal/e2e's
   *  Fingerprint byte for byte: SHA-256 of the two keys concatenated, device
   *  first, cut into six 5-byte chunks, each read as a big-endian integer and
   *  taken modulo 100000. devicePub and hostPub are never "mine" and
   *  "theirs" -- both ends of a pairing call this the same way round, device
   *  first, host second, which is what makes them land on the same code. */
  async function fingerprint(devicePubRaw, hostPubRaw) {
    const data = concatBytes(
      devicePubRaw instanceof Uint8Array ? devicePubRaw : new Uint8Array(devicePubRaw),
      hostPubRaw instanceof Uint8Array ? hostPubRaw : new Uint8Array(hostPubRaw),
    );
    const sum = new Uint8Array(await crypto.subtle.digest("SHA-256", data));
    const groups = [];
    for (let i = 0; i < FINGERPRINT_GROUPS; i++) {
      let v = 0n;
      for (let j = 0; j < FINGERPRINT_GROUP_BYTES; j++) v = (v << 8n) | BigInt(sum[i * FINGERPRINT_GROUP_BYTES + j]);
      groups.push((v % FINGERPRINT_GROUP_MOD).toString().padStart(5, "0"));
    }
    return groups.join(" ");
  }

  // -------------------------------------------------------------- ECDH/HKDF

  async function ecdh(privateKey, publicKey) {
    const bits = await crypto.subtle.deriveBits({ name: "ECDH", public: publicKey }, privateKey, 256);
    return new Uint8Array(bits);
  }

  async function hkdfExpand(ikmKey, salt, infoStr, lengthBits) {
    const info = new TextEncoder().encode(infoStr);
    const bits = await crypto.subtle.deriveBits({ name: "HKDF", hash: "SHA-256", salt, info }, ikmKey, lengthBits);
    return new Uint8Array(bits);
  }

  /** deriveSession derives the session both sides reach independently, given
   *  role, this side's own static and ephemeral key pairs, and the peer's
   *  static and ephemeral public keys -- matching internal/e2e's unexported
   *  newSession, kept visible here (rather than folded into
   *  DeviceHandshake#finish alone) so respondHostHandshake, and this
   *  module's own tests, can drive both sides of the handshake with the
   *  same function. */
  async function deriveSession(role, ownStaticPriv, ownStaticPub, ownEphPriv, ownEphPub, peerStaticPub, peerEphPub) {
    let deviceStaticPub, hostStaticPub, deviceEphPub, hostEphPub, t1, t2, t3;
    if (role === ROLE_DEVICE) {
      deviceStaticPub = ownStaticPub; deviceEphPub = ownEphPub;
      hostStaticPub = peerStaticPub; hostEphPub = peerEphPub;
      t1 = await ecdh(ownEphPriv, hostStaticPub);
      t2 = await ecdh(ownStaticPriv, hostEphPub);
      t3 = await ecdh(ownEphPriv, hostEphPub);
    } else if (role === ROLE_HOST) {
      hostStaticPub = ownStaticPub; hostEphPub = ownEphPub;
      deviceStaticPub = peerStaticPub; deviceEphPub = peerEphPub;
      t1 = await ecdh(ownStaticPriv, deviceEphPub);
      t2 = await ecdh(ownEphPriv, deviceStaticPub);
      t3 = await ecdh(ownEphPriv, deviceEphPub);
    } else {
      throw new Error("e2e: unknown role " + role);
    }

    const ikm = concatBytes(t1, t2, t3);
    const deviceEphRaw = await exportRawPublicKey(deviceEphPub);
    const hostEphRaw = await exportRawPublicKey(hostEphPub);
    const salt = concatBytes(deviceEphRaw, hostEphRaw);
    const ikmKey = await crypto.subtle.importKey("raw", ikm, "HKDF", false, ["deriveBits"]);

    const deviceToHostKeyBytes = await hkdfExpand(ikmKey, salt, INFO_DEVICE_TO_HOST_KEY, KEY_LEN_BITS);
    const hostToDeviceKeyBytes = await hkdfExpand(ikmKey, salt, INFO_HOST_TO_DEVICE_KEY, KEY_LEN_BITS);
    const deviceToHostPrefix = await hkdfExpand(ikmKey, salt, INFO_DEVICE_TO_HOST_NONCE, NONCE_PREFIX_LEN * 8);
    const hostToDevicePrefix = await hkdfExpand(ikmKey, salt, INFO_HOST_TO_DEVICE_NONCE, NONCE_PREFIX_LEN * 8);

    const deviceToHostKey = await crypto.subtle.importKey("raw", deviceToHostKeyBytes, "AES-GCM", false, ["encrypt", "decrypt"]);
    const hostToDeviceKey = await crypto.subtle.importKey("raw", hostToDeviceKeyBytes, "AES-GCM", false, ["encrypt", "decrypt"]);

    let sendKey, sendPrefix, sendRole, recvKey, recvPrefix, recvRole;
    if (role === ROLE_DEVICE) {
      sendKey = deviceToHostKey; sendPrefix = deviceToHostPrefix; sendRole = ROLE_DEVICE;
      recvKey = hostToDeviceKey; recvPrefix = hostToDevicePrefix; recvRole = ROLE_HOST;
    } else {
      sendKey = hostToDeviceKey; sendPrefix = hostToDevicePrefix; sendRole = ROLE_HOST;
      recvKey = deviceToHostKey; recvPrefix = deviceToHostPrefix; recvRole = ROLE_DEVICE;
    }
    return new Session(sendKey, sendPrefix, sendRole, recvKey, recvPrefix, recvRole);
  }

  // ------------------------------------------------------------------ frames

  /** Session is one terminal socket's derived keys, matching internal/e2e's
   *  Session: two AES-256-GCM ciphers, one for each direction, each with its
   *  own nonce prefix and its own frame counter. Good for the life of the
   *  socket it was made for alone -- nothing here ever persists one. */
  class Session {
    constructor(sendKey, sendPrefix, sendRole, recvKey, recvPrefix, recvRole) {
      this.sendKey = sendKey;
      this.sendPrefix = sendPrefix;
      this.sendRole = sendRole;
      this.sendCounter = 0n;
      this.recvKey = recvKey;
      this.recvPrefix = recvPrefix;
      this.recvRole = recvRole;
      this.recvCounter = 0n;
    }

    static nonce(prefix, counter) {
      const n = new Uint8Array(NONCE_PREFIX_LEN + COUNTER_LEN);
      n.set(prefix, 0);
      new DataView(n.buffer).setBigUint64(NONCE_PREFIX_LEN, counter, false);
      return n;
    }

    static aad(sender) { return new Uint8Array([VERSION, sender]); }

    /** seal encrypts plaintext as the next frame of this session's outgoing
     *  direction, matching internal/e2e's Session.Seal byte for byte: a wire
     *  version byte, an 8-byte big-endian counter, then the AEAD's ciphertext
     *  and its 16-byte tag. */
    async seal(plaintext) {
      const counter = this.sendCounter;
      const nonce = Session.nonce(this.sendPrefix, counter);
      const ct = new Uint8Array(await crypto.subtle.encrypt(
        { name: "AES-GCM", iv: nonce, additionalData: Session.aad(this.sendRole), tagLength: 128 },
        this.sendKey, plaintext,
      ));
      const frame = new Uint8Array(1 + COUNTER_LEN + ct.length);
      frame[0] = VERSION;
      new DataView(frame.buffer).setBigUint64(1, counter, false);
      frame.set(ct, 1 + COUNTER_LEN);
      this.sendCounter += 1n;
      return frame;
    }

    /** open authenticates and decrypts a frame seal made of this session's
     *  incoming direction, matching internal/e2e's Session.Open: it refuses
     *  the wrong wire version, a counter that is not exactly the next
     *  expected, or a frame that does not authenticate at all. */
    async open(frame) {
      if (frame.length < 1 + COUNTER_LEN) throw new Error("e2e: frame is shorter than a header");
      if (frame[0] !== VERSION) throw new Error(`e2e: frame is wire version ${frame[0]}, this session speaks ${VERSION}`);
      const dv = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
      const counter = dv.getBigUint64(1, false);
      if (counter !== this.recvCounter) throw new Error(`e2e: frame counter ${counter}, want ${this.recvCounter}`);
      const nonce = Session.nonce(this.recvPrefix, counter);
      const ciphertext = frame.subarray(1 + COUNTER_LEN);
      let plaintext;
      try {
        plaintext = new Uint8Array(await crypto.subtle.decrypt(
          { name: "AES-GCM", iv: nonce, additionalData: Session.aad(this.recvRole), tagLength: 128 },
          this.recvKey, ciphertext,
        ));
      } catch (err) {
        throw new Error("e2e: frame does not authenticate: " + err.message);
      }
      this.recvCounter += 1n;
      return plaintext;
    }
  }

  // -------------------------------------------------------------- handshake

  /** DeviceHandshake is this window's side of opening one terminal session,
   *  from the moment it makes its ephemeral key until the desktop's response
   *  completes it -- matching internal/e2e's DeviceHandshake. Made by
   *  startTerminalHandshake, not directly. */
  class DeviceHandshake {
    constructor(staticPriv, staticPub, ephPriv, ephPub, hostStaticPub) {
      this.staticPriv = staticPriv;
      this.staticPub = staticPub;
      this.ephPriv = ephPriv;
      this.ephPub = ephPub;
      this.hostStaticPub = hostStaticPub;
    }

    /** finish completes this handshake with the desktop's response -- its
     *  ephemeral public key, raw, exactly as the hello was -- and returns the
     *  session the two now independently share. */
    async finish(response) {
      const hostEphPub = await importPublicKeyRaw(response instanceof Uint8Array ? response : new Uint8Array(response));
      return deriveSession(ROLE_DEVICE, this.staticPriv, this.staticPub, this.ephPriv, this.ephPub, this.hostStaticPub, hostEphPub);
    }
  }

  /** startTerminalHandshake begins this window's side of a fresh terminal
   *  session with the desktop whose long-term public key is hostPublicKey
   *  (base64url, from the hello message's own e2ePublicKey) -- matching
   *  internal/e2e's StartDeviceHandshake. Returns the handshake and the hello
   *  message to send as the terminal socket's very first frame, raw and
   *  unencrypted, ahead of anything else. */
  async function startTerminalHandshake(identity, hostPublicKey) {
    const hostStaticPub = await decodePublicKey(hostPublicKey);
    const ephPair = await crypto.subtle.generateKey(ECDH_ALG, true, ["deriveBits"]);
    const hello = await exportRawPublicKey(ephPair.publicKey);
    const handshake = new DeviceHandshake(identity.privateKey, identity.publicKey, ephPair.privateKey, ephPair.publicKey, hostStaticPub);
    return { handshake, hello };
  }

  /** respondHostHandshake is a desktop's whole side of the handshake in one
   *  call, matching internal/e2e's RespondHostHandshake -- exported here only
   *  so this module's own tests can play both sides of a handshake in JS
   *  alone; a real desktop is Go, and uses that package directly through
   *  internal/remote/e2ekey.go. */
  async function respondHostHandshake(hostStaticPriv, hostStaticPub, deviceStaticPub, hello) {
    const deviceEphPub = await importPublicKeyRaw(hello instanceof Uint8Array ? hello : new Uint8Array(hello));
    const hostEphPair = await crypto.subtle.generateKey(ECDH_ALG, true, ["deriveBits"]);
    const session = await deriveSession(ROLE_HOST, hostStaticPriv, hostStaticPub, hostEphPair.privateKey, hostEphPair.publicKey, deviceStaticPub, deviceEphPub);
    const response = await exportRawPublicKey(hostEphPair.publicKey);
    return { session, response };
  }

  // --------------------------------------------------------------- identity

  const DB_NAME = "flockdeck-e2e";
  const DB_VERSION = 1;
  const STORE = "identity";
  const RECORD_ID = "device";

  function openDB() {
    return new Promise((resolve, reject) => {
      let req;
      try {
        req = indexedDB.open(DB_NAME, DB_VERSION);
      } catch (err) {
        reject(err);
        return;
      }
      req.onupgradeneeded = () => { req.result.createObjectStore(STORE); };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error || new Error("e2e: could not open the key store"));
    });
  }

  function idbGet(db, key) {
    return new Promise((resolve, reject) => {
      const req = db.transaction(STORE, "readonly").objectStore(STORE).get(key);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }

  function idbPut(db, key, value) {
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, "readwrite");
      tx.objectStore(STORE).put(value, key);
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
  }

  function cryptoAvailable() {
    return typeof indexedDB !== "undefined" && !!(globalThis.crypto && globalThis.crypto.subtle);
  }

  let identityPromise = null;

  /** getIdentity returns this window's long-term P-256 identity key pair,
   *  generating and persisting one to IndexedDB the first time it is asked
   *  for. The private key is a non-extractable WebCrypto key: generated by,
   *  and usable only through, WebCrypto itself, so nothing that can read
   *  IndexedDB -- a compromised page's own script included -- can ever read
   *  it out.
   *
   *  Returns null where WebCrypto or IndexedDB is not available at all -- an
   *  old browser, or a private context that has disabled storage -- which is
   *  this whole feature quietly not offered rather than failing: every
   *  caller treats a null identity exactly as it treats a desktop with no
   *  key of its own, an unencrypted terminal. */
  function getIdentity() {
    if (identityPromise) return identityPromise;
    identityPromise = loadOrCreateIdentity().catch((err) => {
      identityPromise = null;
      throw err;
    });
    return identityPromise;
  }

  async function loadOrCreateIdentity() {
    if (!cryptoAvailable()) return null;
    const db = await openDB();
    const existing = await idbGet(db, RECORD_ID);
    if (existing && existing.privateKey && existing.publicKeyRaw) {
      const publicKeyRaw = new Uint8Array(existing.publicKeyRaw);
      return { privateKey: existing.privateKey, publicKey: await importPublicKeyRaw(publicKeyRaw), publicKeyRaw };
    }
    const pair = await crypto.subtle.generateKey(ECDH_ALG, false, ["deriveBits"]);
    const publicKeyRaw = await exportRawPublicKey(pair.publicKey);
    await idbPut(db, RECORD_ID, { privateKey: pair.privateKey, publicKeyRaw: publicKeyRaw.buffer.slice(0) });
    return { privateKey: pair.privateKey, publicKey: pair.publicKey, publicKeyRaw };
  }

  let lastRegistered = null;

  /** ensureRegistered registers this window's identity key with the relay
   *  (POST /.flockdeck-e2e-key, deskorigin.go) unless it already has -- safe
   *  to call every time the page starts, or as often as it likes: it does
   *  nothing once the relay is already known to hold this exact key. A
   *  failure (offline, the relay down) is swallowed -- a window that could
   *  not register yet still opens terminals, unencrypted, exactly as one
   *  whose desktop has no key of its own does, and the next successful call
   *  catches it up.
   *
   *  Only meaningful for a window reached through the relay -- app.js calls
   *  this behind its own remoteWindow check, since a window open on the
   *  desktop itself has no relay to register with and nothing to encrypt
   *  against itself. */
  async function ensureRegistered() {
    const identity = await getIdentity();
    if (!identity) return;
    const key = encodePublicKey(identity.publicKeyRaw);
    if (lastRegistered === key) return;
    try {
      const resp = await fetch("/.flockdeck-e2e-key", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ publicKey: key }),
      });
      if (!resp.ok) return;
      lastRegistered = key;
    } catch {
      // Retried the next time this is called -- see the doc comment above.
    }
  }

  window.FlockdeckE2E = {
    VERSION, ROLE_DEVICE, ROLE_HOST,
    encodePublicKey, decodePublicKey, decodePublicKeyRaw,
    fingerprint, deriveSession, Session, DeviceHandshake,
    startTerminalHandshake, respondHostHandshake,
    getIdentity, ensureRegistered,
  };
})();
