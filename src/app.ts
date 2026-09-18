import express from "express";
import { paymentMiddleware, x402ResourceServer } from "@x402/express";
import { ExactEvmScheme } from "@x402/evm/exact/server";
import { HTTPFacilitatorClient } from "@x402/core/server";
import { parseSignature, recoverTypedDataAddress } from "viem";
import { FACILITATOR_URL, kiteChainByName, kiteMoneyParser } from "./kite.js";
import { lookup, parseQuery, type Lookup } from "./dns.js";

export interface Config { payTo: string; network: string; price: string; facilitatorUrl?: string }
const b64 = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64");
async function verifyPyusd(payload: any, accepted: any) {
  const auth = payload?.payload?.authorization;
  const signature = payload?.payload?.signature;
  if (payload?.x402Version !== 2 || !auth || !signature || auth.to?.toLowerCase() !== accepted.payTo.toLowerCase() || BigInt(auth.value) < BigInt(accepted.amount)) throw new Error("invalid payment payload");
  const signer = await recoverTypedDataAddress({
    domain: { name: "PYUSD", version: "1", chainId: 2368, verifyingContract: accepted.asset },
    types: { TransferWithAuthorization: [
      {name:"from",type:"address"},{name:"to",type:"address"},{name:"value",type:"uint256"},
      {name:"validAfter",type:"uint256"},{name:"validBefore",type:"uint256"},{name:"nonce",type:"bytes32"},
    ] }, primaryType: "TransferWithAuthorization", message: auth, signature,
  });
  const rpc = await fetch("https://rpc-testnet.gokite.ai", {method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify({jsonrpc:"2.0",id:1,method:"eth_getBlockByNumber",params:["latest",false]})});
  const block = await rpc.json() as {result?:{timestamp?:string}};
  const now = Number(BigInt(block.result?.timestamp ?? "0x0"));
  if (signer.toLowerCase() !== auth.from.toLowerCase() || now <= Number(auth.validAfter) || now >= Number(auth.validBefore)) throw new Error("invalid or expired signature");
  return { auth, signature };
}
export function createApp(config: Config, resolve: Lookup = lookup) {
  if (!/^0x[0-9a-fA-F]{40}$/.test(config.payTo) || /^0x0{40}$/i.test(config.payTo)) throw new Error("PAY_TO must be a nonzero EVM address");
  if (!/^(0|[1-9]\d*)(\.\d{1,6})?$/.test(config.price) || Number(config.price) <= 0) throw new Error("PRICE_USD must be a positive decimal with at most 6 fractional digits");
  const chain = kiteChainByName(config.network);
  const gasless = config.facilitatorUrl === "gasless";
  const resourceServer = gasless ? undefined : new x402ResourceServer(new HTTPFacilitatorClient({ url: config.facilitatorUrl ?? FACILITATOR_URL })).register(chain.network, new ExactEvmScheme().registerMoneyParser(kiteMoneyParser(chain)));
  const app = express();
  app.disable("x-powered-by");
  app.use((_req, res, next) => {
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Allow-Headers", "Content-Type, Payment-Signature");
    res.setHeader("Access-Control-Expose-Headers", "Payment-Required, Payment-Response");
    next();
  });
  app.options("/v1/dns", (_req, res) => res.sendStatus(204));
  app.get("/healthz", (_req, res) => res.json({ ok: true, network: chain.network, price: config.price }));
  app.use((_req, res, next) => { res.setHeader("Cache-Control", "no-store"); next(); });
  // Reject invalid requests before payment verification. Only this exact GET route is billable.
  app.all("/v1/dns", (req, res, next) => {
    if (req.method !== "GET") { res.setHeader("Allow", "GET"); res.status(405).json({ error: "method_not_allowed" }); return; }
    try { res.locals.query = parseQuery(req.query); next(); }
    catch (err) { res.status(400).json({ error: "invalid_query", message: (err as Error).message }); }
  });
  // Initialize only inside a paid-route request. Workers cancel unfinished I/O
  // when an earlier health request completes; caching that promise would hang later calls.
  let paidMiddleware: ReturnType<typeof paymentMiddleware> | undefined;
  app.get("/v1/dns", (req, res, next) => {
    if (gasless) {
      const amount = (BigInt(Math.round(Number(config.price) * 1e6)) * 10n ** 12n).toString();
      const accepted = {scheme:"exact",network:chain.network,amount,asset:chain.assetAddress,payTo:config.payTo,maxTimeoutSeconds:60,extra:{name:chain.eip712Name,version:chain.eip712Version}};
      const required = {x402Version:2,error:"Payment required",resource:{url:`${req.protocol}://${req.get("host")}${req.originalUrl}`,description:"Live public DNS records for a domain",mimeType:"application/json"},accepts:[accepted]};
      const encoded = req.get("payment-signature");
      if (!encoded) { res.setHeader("Payment-Required",b64(required)); res.status(402).json({}); return; }
      void (async () => {
        try {
          const payload = JSON.parse(Buffer.from(encoded,"base64").toString());
          const {auth,signature} = await verifyPyusd(payload,accepted);
          const {name,type} = res.locals.query;
          const records = await resolve(name,type);
          const {v,r,s} = parseSignature(signature);
          const settled = await fetch("https://gasless.gokite.ai/testnet",{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify({...auth,tokenAddress:chain.assetAddress,v:Number(v),r,s})});
          const result = await settled.json() as {txHash?:string;detail?:string;error?:string};
          if (!settled.ok || !result.txHash) throw new Error(result.detail ?? result.error ?? "transaction_failed");
          let receipt: {status?:string}|undefined;
          for (let i=0;i<20;i++) {
            const rpcResult = await fetch("https://rpc-testnet.gokite.ai",{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify({jsonrpc:"2.0",id:1,method:"eth_getTransactionReceipt",params:[result.txHash]})});
            receipt = ((await rpcResult.json()) as {result?:{status?:string}}).result;
            if (receipt) break;
            await new Promise(resolve=>setTimeout(resolve,500));
          }
          if (!receipt || receipt.status !== "0x1") throw new Error("transaction_reverted_or_unconfirmed");
          res.setHeader("Payment-Response",b64({success:true,payer:auth.from,transaction:result.txHash,network:chain.network}));
          res.json({name,type,records,queried_at:new Date().toISOString()});
        } catch (err) {
          res.setHeader("Payment-Response",b64({success:false,errorReason:(err as Error).message,transaction:"",network:chain.network}));
          res.status(402).json({});
        }
      })();
      return;
    }
    paidMiddleware ??= paymentMiddleware({ "GET /v1/dns": { accepts: { scheme: "exact", price: `$${config.price}`, network: chain.network, payTo: config.payTo, maxTimeoutSeconds: 60 }, description: "Live public DNS records for a domain", mimeType: "application/json" } }, resourceServer!);
    return paidMiddleware(req, res, next);
  });
  app.get("/v1/dns", async (_req, res) => {
    const { name, type } = res.locals.query;
    try {
      const records = await resolve(name, type);
      // SDK buffers this success response and settles before releasing it to the buyer.
      res.json({ name, type, records, queried_at: new Date().toISOString() });
    } catch (err) {
      const code = (err as NodeJS.ErrnoException).code;
      const missing = code === "ENOTFOUND" || code === "ENODATA";
      res.status(missing ? 404 : 502).json({ error: missing ? "dns_record_not_found" : "dns_lookup_failed" });
    }
  });
  app.use((_req, res) => res.status(404).json({ error: "not_found" }));
  return app;
}
