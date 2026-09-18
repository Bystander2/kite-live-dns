import { once } from 'node:events';
import { writeFile } from 'node:fs/promises';
import { createApp } from '../dist/app.js';
import { lookup } from '../dist/dns.js';
// Non-owned fixture address: only request an unpaid challenge. Never pay this address.
const payTo = '0x1111111111111111111111111111111111111111';
const app = createApp({payTo,network:'testnet',price:'0.001'});
const server = app.listen(0,'127.0.0.1');
await once(server,'listening');
try {
  const response = await fetch(`http://127.0.0.1:${server.address().port}/v1/dns?name=example.com&type=A`,{signal:AbortSignal.timeout(20000)});
  const header = response.headers.get('payment-required');
  if(response.status!==402 || !header) throw new Error(`Expected 402 challenge, got ${response.status}`);
  const challenge = JSON.parse(Buffer.from(header,'base64').toString());
  if(challenge.accepts[0].network!=='eip155:2368') throw new Error('Wrong network');
  const records = await lookup('example.com','A');
  const report = {recorded_at:new Date().toISOString(),scope:'Live unpaid challenge + independent DNS lookup; NOT a paid end-to-end call',receiving_address:'fixture only, not the user wallet',http_status:response.status,challenge,dns:{name:'example.com',type:'A',records},on_chain_payment_performed:false};
  await writeFile(new URL('../evidence/live-unpaid-smoke.json',import.meta.url),JSON.stringify(report,null,2)+'\n');
  console.log('PASS: live facilitator 402 challenge and independent real DoH lookup; no payment performed.');
} finally { server.closeAllConnections(); await new Promise(r=>server.close(r)); }
