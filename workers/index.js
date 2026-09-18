import { httpServerHandler } from 'cloudflare:node';
import { createApp } from '../dist/app.js';

let handler;
export default {
  fetch(request, env, ctx) {
    // x402 initialization performs HTTP requests: initialize inside request context,
    // never during Workers module evaluation where outbound I/O is prohibited.
    if (!handler) {
      const app = createApp({
        payTo: env.PAY_TO ?? '',
        network: env.KITE_NETWORK ?? 'testnet',
        price: env.PRICE_USD ?? '0.01',
        facilitatorUrl: env.FACILITATOR_URL,
      });
      app.listen(8080);
      handler = httpServerHandler({ port: 8080 });
    }
    return handler.fetch(request, env, ctx);
  },
};
