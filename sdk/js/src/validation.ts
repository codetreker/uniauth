import { Buffer } from "node:buffer";
import { BlockList, isIP } from "node:net";

export const validText = (value: unknown, maxBytes: number): value is string =>
  typeof value === "string" &&
  value.length > 0 &&
  value.isWellFormed() &&
  Buffer.byteLength(value, "utf8") <= maxBytes;

export const requireText = (
  value: unknown,
  maxBytes: number,
  name: string,
): string => {
  if (!validText(value, maxBytes)) {
    throw new TypeError(
      `${name} must contain 1 to ${maxBytes} valid UTF-8 bytes.`,
    );
  }
  return value;
};

const loopbackAddresses = new BlockList();
loopbackAddresses.addSubnet("127.0.0.0", 8, "ipv4");
loopbackAddresses.addAddress("::1", "ipv6");

const loopback = (authority: string): boolean => {
  const host = authority.startsWith("[")
    ? authority.slice(1, authority.indexOf("]"))
    : authority.split(":", 1)[0];
  if (host === undefined) return false;
  if (host.toLowerCase() === "localhost") return true;
  const family = isIP(host);
  return (
    family !== 0 &&
    loopbackAddresses.check(host, family === 4 ? "ipv4" : "ipv6")
  );
};

export const validateURL = (
  raw: string,
  allowLoopbackHTTP: boolean,
  originOnly: boolean,
): URL => {
  const name = originOnly ? "baseURL" : "redirectURI";
  if (typeof raw !== "string" || !raw.isWellFormed()) {
    throw new TypeError(`${name} must be a valid URL.`);
  }
  const parts = /^(https|http):\/\/([^/?#]*)([^?#]*)(?:\?([^#]*))?$/.exec(raw);
  if (
    parts === null ||
    /[\x00-\x1f\x7f]/.test(raw) ||
    /%(?![\da-f]{2})/i.test(raw)
  ) {
    throw new TypeError(`${name} must be a valid URL without a fragment.`);
  }
  const authority = parts[2]!;
  if (authority === "" || /[@\\\s]/.test(authority)) {
    throw new TypeError(`${name} requires a host and excludes credentials.`);
  }
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new TypeError(`${name} must be a valid URL.`);
  }
  if (
    parsed.protocol !== "https:" &&
    !(allowLoopbackHTTP && parsed.protocol === "http:" && loopback(authority))
  ) {
    throw new TypeError(`${name} requires HTTPS or enabled loopback HTTP.`);
  }
  if (originOnly) {
    if ((parts[3] !== "" && parts[3] !== "/") || parts[4] !== undefined) {
      throw new TypeError(
        "baseURL must be an origin without a query or fragment.",
      );
    }
  } else {
    if (parts[4]?.includes(";")) {
      throw new TypeError("redirectURI query parameters are invalid.");
    }
    for (const reserved of ["code", "state", "error"]) {
      if (parsed.searchParams.has(reserved)) {
        throw new TypeError("redirectURI contains a reserved query parameter.");
      }
    }
  }
  return parsed;
};
