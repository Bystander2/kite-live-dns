import { domainToASCII } from "node:url";
import { isIP } from "node:net";

export const RECORD_TYPES = ["A", "AAAA", "MX", "TXT", "NS", "CNAME", "SOA", "CAA"] as const;
export type RecordType = typeof RECORD_TYPES[number];
export type Lookup = (name: string, type: RecordType) => Promise<unknown>;
export function parseQuery(query: Record<string, unknown>): { name: string; type: RecordType } {
  if (typeof query.name !== "string" || (query.type !== undefined && typeof query.type !== "string")) throw new Error("name must be a domain; type must be one record type");
  const raw = query.name.trim().replace(/\.$/, "");
  if (/[\s\/\\?#@:％%]/u.test(raw)) throw new Error("name must not contain URL components or escapes");
  const name = domainToASCII(raw).toLowerCase();
  const type = String(query.type ?? "A").toUpperCase() as RecordType;
  if (!name || name.length > 253 || isIP(name) || !name.includes(".") || !name.split(".").every(label => /^[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?$/.test(label))) throw new Error("name must be a valid fully qualified domain name");
  if (!RECORD_TYPES.includes(type)) throw new Error(`type must be ${RECORD_TYPES.join(", ")}`);
  return { name, type };
}
export function createLookup(request: typeof fetch = fetch): Lookup {
  return async (name, type) => {
    const url = new URL("https://dns.google/resolve");
    url.searchParams.set("name", name);
    url.searchParams.set("type", type);
    url.searchParams.set("edns_client_subnet", "0.0.0.0/0");
    // Workers does not implement redirect:"error"; manual still prevents following
    // an upstream redirect, and the non-2xx check below rejects it.
    const response = await request(url, { signal: AbortSignal.timeout(6000), redirect: "manual", headers: { Accept: "application/dns-json" } });
    if (!response.ok) throw new Error("DNS upstream HTTP error");
    const data = await response.json() as { Status?: number; TC?: boolean; Answer?: Array<{ name: string; type: number; TTL: number; data: string }> };
    if (data.Status === 3) throw Object.assign(new Error("No domain"), {code: "ENOTFOUND"});
    if (data.Status !== 0 || data.TC) throw new Error("DNS upstream failed or truncated");
    if (!data.Answer?.length) throw Object.assign(new Error("No records"), {code: "ENODATA"});
    if (!Array.isArray(data.Answer) || !data.Answer.every(r => typeof r.name === "string" && Number.isInteger(r.type) && Number.isInteger(r.TTL) && r.TTL >= 0 && typeof r.data === "string")) throw new Error("Invalid DNS upstream response");
    return data.Answer.map(r => ({ name: r.name, type: r.type, ttl: r.TTL, data: r.data }));
  };
}
export const lookup = createLookup();
