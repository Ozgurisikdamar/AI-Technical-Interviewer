/**
 * The message a catalog holds for a code, or undefined. Codes come from the server, so only the
 * catalog's own keys count: `constructor` or `toString` never resolve to built-in object members.
 */
export function label(catalog: object, key: string | number): string | undefined {
  if (!Object.hasOwn(catalog, key)) return undefined;
  const value: unknown = (catalog as Record<string | number, unknown>)[key];
  return typeof value === 'string' ? value : undefined;
}
