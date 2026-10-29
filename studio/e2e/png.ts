/**
 * A minimal PNG encoder for the e2e tests: `glossa capture` uploads real
 * screenshots, and the Captures API re-encodes what it stores, so the
 * tests need actual PNG bytes — a few striped pixels are enough, and
 * generating them keeps a binary fixture out of the repository.
 */
import { createHash } from "node:crypto";
import { deflateSync } from "node:zlib";

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(buf: Buffer): number {
  let c = 0xffffffff;
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff]! ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([length, body, crc]);
}

/** An 8-bit RGB PNG of `width`×`height`, coloured by `pixel`. */
export function png(width: number, height: number, pixel: (x: number, y: number) => [number, number, number]): Buffer {
  const raw = Buffer.alloc(height * (1 + width * 3));
  let at = 0;
  for (let y = 0; y < height; y++) {
    raw[at++] = 0; // filter: none
    for (let x = 0; x < width; x++) {
      const [r, g, b] = pixel(x, y);
      raw[at++] = r;
      raw[at++] = g;
      raw[at++] = b;
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // colour type: truecolour
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

/** The lowercase hex SHA-256 of some bytes: what a capture's image part is named after. */
export const sha256 = (bytes: Buffer): string => createHash("sha256").update(bytes).digest("hex");

/**
 * A page-like screenshot: a light page with a darker band where a
 * message sits, so a crop around the region is visibly different from
 * the rest of the page.
 */
export function pageShot(width: number, height: number, band: { y: number; height: number }): Buffer {
  return png(width, height, (_x, y) => (y >= band.y && y < band.y + band.height ? [37, 99, 235] : [244, 244, 245]));
}
