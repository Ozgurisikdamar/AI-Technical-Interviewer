/**
 * Small runtime checks shared by the response parsers. Each throws a ResponseShapeError naming the
 * offending field (never its value), which the client turns into an `invalid_response` ApiError.
 */

export class ResponseShapeError extends Error {
  constructor(readonly field: string) {
    super(`Unexpected response shape at ${field}`);
    this.name = 'ResponseShapeError';
  }
}

export type Json = Record<string, unknown>;

export function record(value: unknown, field: string): Json {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new ResponseShapeError(field);
  return value as Json;
}

export function string(obj: Json, key: string, field: string, maxLength = 10_000): string {
  const value = obj[key];
  if (typeof value !== 'string' || value.length > maxLength) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

export function nonEmptyString(obj: Json, key: string, field: string, maxLength = 10_000): string {
  const value = string(obj, key, field, maxLength);
  if (value.length === 0) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

export function integer(obj: Json, key: string, field: string, min: number): number {
  const value = obj[key];
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min) {
    throw new ResponseShapeError(`${field}.${key}`);
  }
  return value;
}

export function boolean(obj: Json, key: string, field: string): boolean {
  const value = obj[key];
  if (typeof value !== 'boolean') throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

export function array(obj: Json, key: string, field: string, maxItems = 200): unknown[] {
  const value = obj[key];
  if (!Array.isArray(value) || value.length > maxItems) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}
