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
  if (plan === "scale") {
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
