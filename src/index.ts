import { createApp } from "./app.js";
const app = createApp({ payTo: process.env.PAY_TO ?? "", network: process.env.KITE_NETWORK ?? "testnet", price: process.env.PRICE_USD ?? "0.01", facilitatorUrl: process.env.FACILITATOR_URL });
const port = Number(process.env.PORT ?? 8080);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("PORT must be 1..65535");
const server = app.listen(port, "0.0.0.0", () => console.log(`Live DNS API listening on ${port}`));
for (const signal of ["SIGTERM", "SIGINT"] as const) process.on(signal, () => { server.close(); });
