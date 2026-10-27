// Pictures for a structured pane's prompt box. A picture pasted, dropped or chosen is
// sent to the desktop with attachImage, which saves it under Flockdeck's own state
// directory and answers with the path; the path goes into the prompt as text, and
// the agent reads the file. The box takes a picture only when the agent said it
// takes images (capabilities.image of the push).
//
// The control socket carries 1 MiB a message, so a large picture is scaled down
// and re-encoded here first. Only the type the browser reports is trusted to pick a
// route, and the desktop checks the bytes again.

/** IMAGE_TYPES are the types the box takes. */
export const IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp", "image/gif"];
/** SEND_MAX is the most bytes of a picture sent as it is, and the most after scaling; base64 makes it a third more. */
export const SEND_MAX = 600 * 1024;
/** SOURCE_MAX is the largest picture that is read at all. */
export const SOURCE_MAX = 25 * 1024 * 1024;
export const SIDE_MAX = 1600;
/** DIM_REFUSE and PIXELS_REFUSE are the sides and the area, in pixels, above which a picture is refused before it is decoded: a
 *  small file can declare a huge image, and decoding it takes memory in proportion to the pixels. */
export const DIM_REFUSE = 8000;
export const PIXELS_REFUSE = 40e6;
export const HEAD_BYTES = 256 * 1024;
export const NAME_MAX = 80;

/** ImageError is a failure whose message is fit to show the person. Anything else that goes wrong is
 *  said in general words. */
export class ImageError extends Error {}

/** planImage says what to do with a picture of this type and size: "send" as it is, "scale" it down
 *  first, or "refuse" (not a picture type the box takes, or too large to read). */
export function planImage(type, size) {
  if (!IMAGE_TYPES.includes(type) || !Number.isFinite(size) || size <= 0 || size > SOURCE_MAX) return "refuse";
  return size <= SEND_MAX ? "send" : "scale";
}

/** sizeOf reads the width and height a picture declares in its header: { w, h }, or null when the
 *  bytes are not a header of PNG, GIF, JPEG or WebP this can read. It looks at the bytes, not at the
 *  type the browser reported. */
export function sizeOf(bytes) {
  const b = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  const u16 = (i) => (b[i] << 8) | b[i + 1];
  const u32 = (i) => ((b[i] * 0x1000000) + (b[i + 1] << 16) + (b[i + 2] << 8) + b[i + 3]);
  const ascii = (i, n) => String.fromCharCode(...b.subarray(i, i + n));
  if (b.length >= 24 && b[0] === 0x89 && ascii(1, 3) === "PNG") return { w: u32(16), h: u32(20) };
  if (b.length >= 10 && ascii(0, 3) === "GIF") return { w: b[6] | (b[7] << 8), h: b[8] | (b[9] << 8) };
  if (b.length >= 30 && ascii(0, 4) === "RIFF" && ascii(8, 4) === "WEBP") {
    const kind = ascii(12, 4);
    if (kind === "VP8X") return { w: 1 + (b[24] | (b[25] << 8) | (b[26] << 16)), h: 1 + (b[27] | (b[28] << 8) | (b[29] << 16)) };
    if (kind === "VP8L") {
      const v = b[21] | (b[22] << 8) | (b[23] << 16) | (b[24] * 0x1000000);
      return { w: 1 + (v & 0x3fff), h: 1 + ((v >>> 14) & 0x3fff) };
    }
    if (kind === "VP8 ") return { w: (b[26] | (b[27] << 8)) & 0x3fff, h: (b[28] | (b[29] << 8)) & 0x3fff };
    return null;
  }
  if (b.length >= 4 && b[0] === 0xff && b[1] === 0xd8) {
    let i = 2;
    while (i + 9 < b.length) {
      if (b[i] !== 0xff) { i++; continue; }
      const m = b[i + 1];
      if (m === 0xff) { i++; continue; }
      if (m >= 0xc0 && m <= 0xcf && m !== 0xc4 && m !== 0xc8 && m !== 0xcc) return { w: u16(i + 7), h: u16(i + 5) };
      if (m === 0xd8 || (m >= 0xd0 && m <= 0xd7) || m === 0x01) { i += 2; continue; }
      i += 2 + u16(i + 2);
    }
  }
  return null;
}

/** tooBig says a declared size is past what is decoded. */
export const tooBig = (d) => d.w < 1 || d.h < 1 || d.w > DIM_REFUSE || d.h > DIM_REFUSE || d.w * d.h > PIXELS_REFUSE;

/** imageName is a short name for the saved file: the picture's own name cut to NAME_MAX characters
 *  without path parts or control characters, or "image" when it has none. */
export function imageName(name) {
  const base = typeof name === "string" ? name.split(/[\\/]/).pop() : "";
  const clean = Array.from(base.replace(/[\p{C}]/gu, "")).slice(0, NAME_MAX).join("").trim();
  return clean || "image";
}

/** attachNote is the text put into the draft for a saved picture. */
export const attachNote = (path) => "[image: " + String(path).replace(/[\p{C}]/gu, "") + "]";

/** pickImages is the pictures in a clipboard or drop (a DataTransfer): files of a type the box takes,
 *  at most max. */
export function pickImages(data, max = 1) {
  const files = data && data.files ? Array.from(data.files) : [];
  return files.filter((f) => f && IMAGE_TYPES.includes(f.type)).slice(0, max);
}

const toBase64 = (bytes) => {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(s);
};

async function scaled(file, env) {
  const bmp = await env.createImageBitmap(file);
  let side = SIDE_MAX;
  for (let attempt = 0; attempt < 4; attempt++) {
    const k = Math.min(1, side / Math.max(bmp.width, bmp.height));
    const canvas = env.makeCanvas(Math.max(1, Math.round(bmp.width * k)), Math.max(1, Math.round(bmp.height * k)));
    canvas.draw(bmp);
    const blob = await canvas.toBlob("image/jpeg", attempt < 2 ? 0.85 : 0.6);
    if (blob && blob.size > 0 && blob.size <= SEND_MAX) {
      if (bmp.close) bmp.close();
      return blob;
    }
    side = Math.round(side / 1.6);
  }
  if (bmp.close) bmp.close();
  throw new ImageError("That picture is too large to attach");
}

/** prepareImage is { name, mediaType, data } for attachImage: the picture as it is if small, else
 *  scaled to fit. It rejects with an Error whose message is for the person. env is the browser's
 *  doing, passed so a test can stand in: createImageBitmap(file) and makeCanvas(w, h), whose result
 *  has draw(bitmap) and toBlob(type, quality). */
export async function prepareImage(file, env = browserEnv()) {
  const plan = planImage(file && file.type, file && file.size);
  if (plan === "refuse") throw new ImageError("That is not a picture the agent can take");
  let blob = file;
  let mediaType = file.type;
  // The header says how many pixels there are; look before anything decodes the picture.
  const head = typeof file.slice === "function" ? file.slice(0, HEAD_BYTES) : file;
  const dims = sizeOf(await head.arrayBuffer());
  if (dims && tooBig(dims)) throw new ImageError("That picture is too many pixels to attach");
  if (plan === "scale") {
    if (!dims) throw new ImageError("That picture's size could not be read");
    if (!env || !env.createImageBitmap) throw new ImageError("This window cannot scale a large picture down");
    blob = await scaled(file, env);
    mediaType = "image/jpeg";
  }
  const bytes = new Uint8Array(await blob.arrayBuffer());
  if (bytes.length > SEND_MAX) throw new ImageError("That picture is too large to attach");
  return { name: imageName(file.name), mediaType, data: toBase64(bytes) };
}

function browserEnv() {
  if (typeof createImageBitmap !== "function" || typeof document === "undefined") return null;
  return {
    createImageBitmap: (f) => createImageBitmap(f),
    makeCanvas(w, h) {
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      return {
        draw(bmp) { c.getContext("2d").drawImage(bmp, 0, 0, w, h); },
        toBlob: (type, q) => new Promise((res) => c.toBlob(res, type, q)),
      };
    },
  };
}
