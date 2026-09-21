import { Container } from "@cloudflare/containers";

export interface Env {
  LIVE_DNS: DurableObjectNamespace<LiveDNSContainer>;
}

export class LiveDNSContainer extends Container {
  defaultPort = 8080;
  sleepAfter = "2m";
  envVars = {
    PAY_TO: "0xa5d1f687B741af9b2B7c2B0D77757C6a0De69055",
    PRICE_USD: "0.01",
    PORT: "8080",
  };
}

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    return env.LIVE_DNS.getByName("live-dns").fetch(request);
  },
};
