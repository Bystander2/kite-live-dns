import { parseSignature, recoverTypedDataAddress } from 'viem';

const NETWORK = 'eip155:2368';
const TOKEN = '0x8E04D099b1a8Dd20E6caD4b2Ab2B405B98242ec9';
const TYPES = {
  TransferWithAuthorization: [
    { name: 'from', type: 'address' }, { name: 'to', type: 'address' },
    { name: 'value', type: 'uint256' }, { name: 'validAfter', type: 'uint256' },
    { name: 'validBefore', type: 'uint256' }, { name: 'nonce', type: 'bytes32' },
  ],
};
const json = (body, status=200) => new Response(JSON.stringify(body), {status, headers:{'content-type':'application/json'}});

async function verify(body) {
  const payment = body?.paymentPayload;
  const requirement = body?.paymentRequirements;
  const auth = payment?.payload?.authorization;
  const signature = payment?.payload?.signature;
  if (payment?.x402Version !== 2 || requirement?.network !== NETWORK || requirement?.asset?.toLowerCase() !== TOKEN.toLowerCase() || !auth || !signature) {
    return { isValid:false, invalidReason:'invalid_payment_requirements', payer:auth?.from ?? '' };
  }
  if (auth.to.toLowerCase() !== requirement.payTo.toLowerCase() || BigInt(auth.value) < BigInt(requirement.amount)) {
    return { isValid:false, invalidReason:'invalid_payment_amount_or_recipient', payer:auth.from };
  }
  const signer = await recoverTypedDataAddress({
    domain:{name:'PYUSD', version:'1', chainId:2368, verifyingContract:TOKEN},
    types:TYPES, primaryType:'TransferWithAuthorization', message:auth, signature,
  });
  const now = Math.floor(Date.now()/1000);
  if (signer.toLowerCase() !== auth.from.toLowerCase() || now <= Number(auth.validAfter) || now >= Number(auth.validBefore)) {
    return { isValid:false, invalidReason:'invalid_or_expired_signature', payer:auth.from };
  }
  return { isValid:true, payer:auth.from };
}

export default {
  async fetch(request) {
    const path = new URL(request.url).pathname;
    if (request.method === 'GET' && path.endsWith('/supported')) return json({kinds:[{x402Version:2,scheme:'exact',network:NETWORK}],extensions:[],signers:{'eip155:*':['0x12343e649e6b2b2b77649DFAb88f103c02F3C78b']}});
    if (request.method !== 'POST' || (!path.endsWith('/verify') && !path.endsWith('/settle'))) return json({error:'not_found'},404);
    const body = await request.json();
    const checked = await verify(body);
    if (path.endsWith('/verify') || !checked.isValid) return json(checked);
    const auth = body.paymentPayload.payload.authorization;
    const {v,r,s} = parseSignature(body.paymentPayload.payload.signature);
    const upstream = await fetch('https://gasless.gokite.ai/testnet', {
      method:'POST', headers:{'content-type':'application/json'},
      body:JSON.stringify({...auth, tokenAddress:TOKEN, v:Number(v), r, s}),
    });
    const result = await upstream.json().catch(()=>({}));
    if (!upstream.ok || !result.txHash) return json({success:false,errorReason:result.detail ?? result.error ?? 'transaction_failed',payer:auth.from,transaction:'',network:NETWORK});
    return json({success:true,payer:auth.from,transaction:result.txHash,network:NETWORK});
  }
};
