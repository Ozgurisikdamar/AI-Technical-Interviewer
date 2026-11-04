/**
 * The shape of a staff token from ati-admin, checked before anything is sent: a malformed paste
 * (an id, a hash, a token with a line break in the middle) is caught here, and the token itself is
 * never stored, logged or put in an address.
 */
const STAFF_TOKEN = /^ati_st_[A-Za-z0-9_-]{20,120}$/;

/** Removes the surrounding whitespace a paste often carries. */
export function cleanToken(raw: string): string {
  return raw.trim();
}

export function isStaffTokenFormat(token: string): boolean {
  return STAFF_TOKEN.test(token);
}
