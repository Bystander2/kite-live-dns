import { test } from 'node:test';
import assert from 'node:assert/strict';
import express from 'express';
import { once } from 'node:events';
import { createApp } from '../src/app.js';
import { parseQuery, type Lookup } from '../src/dns.js';

const payTo = '0x1111111111111111111111111111111111111111';
async function fixture(t: any, options: { dnsFail?: string; reject?: boolean; settleFail?: boolean } = {}) {
  const calls: string[] = [];
  const facilitator = express(); facilitator.use(express.json());
  facilitator.get('/supported', (_req,res) => res.json({ kinds: [{ x402Version: 2, scheme: 'exact', network: 'eip155:2368' }], extensions: [], signers: {} }));
  facilitator.post('/verify', (_req,res) => { calls.push('verify'); res.json({ isValid: !options.reject, payer: payTo, ...(options.reject ? {invalidReason:'invalid_signature'} : {}) }); });
  facilitator.post('/settle', (_req,res) => { calls.push('settle'); res.json({ success: !options.settleFail, transaction: options.settleFail ? '' : '0xmock-not-a-real-transaction', network: 'eip155:2368', ...(options.settleFail ? {errorReason:'insufficient_funds'} : {}) }); });
  const fs = facilitator.listen(0, '127.0.0.1'); await once(fs,'listening');
  const resolve: Lookup = async () => { calls.push('dns'); if(options.dnsFail) throw Object.assign(new Error('test failure'),{code:options.dnsFail}); return ['93.184.215.14']; };
  const app = createApp({payTo,network:'testnet',price:'0.01',facilitatorUrl:`http://127.0.0.1:${(fs.address() as any).port}`},resolve);
  const server = app.listen(0,'127.0.0.1'); await once(server,'listening');
  t.after(async () => { await Promise.all([new Promise<void>(r=>server.close(()=>r())),new Promise<void>(r=>fs.close(()=>r()))]); });
  const base = `http://127.0.0.1:${(server.address() as any).port}`;
  const url = `${base}/v1/dns?name=example.com&type=A`;
  async function paid() {
    const challenge = await fetch(url);
    const required = JSON.parse(Buffer.from(challenge.headers.get('payment-required')!, 'base64').toString());
    const payload = {x402Version:2, resource:required.resource, accepted:required.accepts[0], payload:{signature:'test-only',authorization:{from:payTo}}};
    return fetch(url,{headers:{'payment-signature':Buffer.from(JSON.stringify(payload)).toString('base64')}});
  }
  return {calls,base,url,paid};
}
test('validates domain, IDN, record types and rejects URL/IP/array input',()=>{
  assert.deepEqual(parseQuery({name:'EXAMPLE.COM.',type:'mx'}),{name:'example.com',type:'MX'});
  assert.equal(parseQuery({name:'例子.中国'}).name,'xn--fsqu00a.xn--fiqs8s');
  for(const query of [{name:'http://example.com'},{name:'example.com/path'},{name:'example.com?x=y'},{name:'example.com#x'},{name:'example.com%2fpath'},{name:'127.0.0.1'},{name:'localhost'},{name:'-bad.example'},{name:['example.com']},{name:'example.com',type:'ANY'}]) assert.throws(()=>parseQuery(query));
});
test('unpaid request gets correct Kite testnet 402 without DNS/settlement',async t=>{
  const f=await fixture(t); const res=await fetch(f.url); assert.equal(res.status,402);
  const body=JSON.parse(Buffer.from(res.headers.get('payment-required')!,'base64').toString());
  assert.equal(body.accepts[0].network,'eip155:2368'); assert.equal(body.accepts[0].amount,'10000000000000000'); assert.equal(body.accepts[0].asset,'0x8E04D099b1a8Dd20E6caD4b2Ab2B405B98242ec9'); assert.equal(body.accepts[0].payTo,payTo); assert.deepEqual(f.calls,[]);
});
test('paid request executes verify -> DNS -> settle and returns records',async t=>{
  const f=await fixture(t); const res=await f.paid(); assert.equal(res.status,200); assert.ok(res.headers.get('payment-response')); assert.deepEqual((await res.json()).records,['93.184.215.14']); assert.deepEqual(f.calls,['verify','dns','settle']);
});
for(const code of ['ENOTFOUND','ENODATA','ETIMEOUT']) test(`${code} does not settle`,async t=>{
  const f=await fixture(t,{dnsFail:code}); const res=await f.paid(); assert.equal(res.status,code==='ETIMEOUT'?502:404); assert.deepEqual(f.calls,['verify','dns']); assert.equal(res.headers.get('payment-response'),null);
});
test('invalid authorization cannot invoke DNS or settle',async t=>{
  const f=await fixture(t,{reject:true}); assert.equal((await f.paid()).status,402); assert.deepEqual(f.calls,['verify']);
});
test('settlement failure does not release a successful DNS result',async t=>{
  const f=await fixture(t,{settleFail:true}); const res=await f.paid(); assert.notEqual(res.status,200); assert.equal((await res.text()).includes('93.184.215.14'),false); assert.deepEqual(f.calls,['verify','dns','settle']);
});
test('bad query, HEAD, POST and unknown routes cannot charge',async t=>{
  const f=await fixture(t); assert.equal((await fetch(f.base+'/v1/dns?name=localhost')).status,400);
  for(const method of ['POST','HEAD']) assert.equal((await fetch(f.url,{method})).status,405);
  assert.equal((await fetch(f.base+'/v1/other')).status,404); assert.equal((await fetch(f.base+'/healthz')).status,200); assert.deepEqual(f.calls,[]);
});
test('rejects invalid receiving address and prices',()=>{
  for(const config of [{payTo:'0x'+'0'.repeat(40),price:'0.001'},{payTo,price:'0'},{payTo,price:'0.0000001'}]) assert.throws(()=>createApp({...config,network:'testnet'}));
});

test('DoH stays on fixed HTTPS origin, strips client subnet and normalizes records',async()=>{
  const {createLookup}=await import('../src/dns.js');
  const run=createLookup(async (input,init)=>{
    const url=new URL(String(input)); assert.equal(url.origin,'https://dns.google'); assert.equal(url.searchParams.get('edns_client_subnet'),'0.0.0.0/0'); assert.equal(init?.redirect,'manual');
    return Response.json({Status:0,Answer:[{name:'example.com.',type:1,TTL:30,data:'93.184.215.14'}]});
  });
  assert.deepEqual(await run('example.com','A'),[{name:'example.com.',type:1,ttl:30,data:'93.184.215.14'}]);
});
for(const payload of [{Status:2},{Status:0,TC:true},{Status:0,Answer:[{data:'broken'}]}]) test('DoH rejects failed or malformed answers '+JSON.stringify(payload),async()=>{
  const {createLookup}=await import('../src/dns.js'); await assert.rejects(createLookup(async()=>Response.json(payload))('example.com','A'));
});

test('accepts DNS TXT owner labels for DMARC and DKIM',()=>{
  for(const name of ['_dmarc.example.com','selector._domainkey.example.com']) assert.equal(parseQuery({name,type:'TXT'}).name,name);
});
