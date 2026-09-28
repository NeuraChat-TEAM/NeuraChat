'use strict';
const http = require('node:http');
const http2 = require('node:http2');

const token = process.env.NEURA_H2_TOKEN || '';
const parentPid = Number(process.env.NEURA_PARENT_PID || 0);
const blocked = new Set(['host','connection','proxy-connection','keep-alive','transfer-encoding','upgrade','http2-settings','content-length','accept-encoding']);

function readBody(req, limit = 4 * 1024 * 1024) {
  return new Promise((resolve, reject) => {
    const chunks = []; let size = 0;
    req.on('data', chunk => { size += chunk.length; if (size > limit) { reject(new Error('request too large')); req.destroy(); } else chunks.push(chunk); });
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}
function reply(res, status, value) {
  const body = Buffer.from(JSON.stringify(value));
  res.writeHead(status, {'content-type':'application/json','content-length':String(body.length)});
  res.end(body);
}
async function h2Post(input) {
  const target = new URL(input.path, input.origin);
  const payload = Buffer.from(JSON.stringify(input.body ?? {}));
  const headers = {':method':'POST',':scheme':target.protocol.slice(0,-1),':authority':target.host,':path':target.pathname + target.search};
  for (const [name,value] of Object.entries(input.headers || {})) {
    const lower = name.toLowerCase();
    if (!blocked.has(lower) && value != null && value !== '') headers[lower] = String(value);
  }
  headers['content-length'] = String(payload.length);
  return await new Promise((resolve, reject) => {
    const session = http2.connect(target.origin, {servername: target.hostname, ALPNProtocols:['h2']});
    let settled = false;
    const fail = error => { if (!settled) { settled = true; session.destroy(); reject(error); } };
    session.once('error', fail);
    const request = session.request(headers);
    const chunks = []; let responseHeaders = {};
    request.once('response', h => { responseHeaders = h; });
    request.on('data', chunk => chunks.push(Buffer.from(chunk)));
    request.once('error', fail);
    request.once('end', () => {
      if (settled) return;
      settled = true;
      const status = Number(responseHeaders[':status'] || 0);
      const cleanHeaders = Object.fromEntries(Object.entries(responseHeaders).filter(([key]) => !key.startsWith(':')).map(([key,value]) => [key, Array.isArray(value) ? value.join(', ') : String(value)]));
      session.close();
      resolve({status, headers: cleanHeaders, body: Buffer.concat(chunks).toString('utf8')});
    });
    request.setTimeout(5 * 60 * 1000, () => fail(new Error('HTTP/2 timeout')));
    request.end(payload);
  });
}
async function h2Stream(input, res) {
  const target = new URL(input.path, input.origin);
  const payload = Buffer.from(JSON.stringify(input.body ?? {}));
  const headers = {':method':'POST',':scheme':target.protocol.slice(0,-1),':authority':target.host,':path':target.pathname + target.search};
  for (const [name,value] of Object.entries(input.headers || {})) {
    const lower = name.toLowerCase();
    if (!blocked.has(lower) && value != null && value !== '') headers[lower] = String(value);
  }
  headers['content-length'] = String(payload.length);
  await new Promise((resolve, reject) => {
    const session = http2.connect(target.origin, {servername: target.hostname, ALPNProtocols:['h2']});
    let settled = false;
    const finish = error => {
      if (settled) return;
      settled = true;
      if (error) session.destroy(); else session.close();
      error ? reject(error) : resolve();
    };
    session.once('error', finish);
    const request = session.request(headers);
    request.once('response', upstream => {
      const status = Number(upstream[':status'] || 502);
      const clean = {};
      for (const [key,value] of Object.entries(upstream)) {
        if (!key.startsWith(':') && !blocked.has(key.toLowerCase()) && key.toLowerCase() !== 'content-encoding') {
          clean[key] = Array.isArray(value) ? value.join(', ') : String(value);
        }
      }
      res.writeHead(status, clean);
    });
    request.on('data', chunk => {
      if (!res.destroyed) res.write(chunk);
    });
    request.once('error', finish);
    request.once('end', () => {
      if (!res.destroyed) res.end();
      finish();
    });
    res.once('close', () => {
      if (!settled) {
        request.close(http2.constants.NGHTTP2_CANCEL);
        finish();
      }
    });
    request.setTimeout(5 * 60 * 1000, () => finish(new Error('HTTP/2 timeout')));
    request.end(payload);
  });
}

const server = http.createServer(async (req,res) => {
  try {
    if (req.url === '/health') return reply(res,200,{ok:true});
    if (req.headers['x-neura-token'] !== token) return reply(res,401,{error:'unauthorized'});
    if (req.method !== 'POST' || (req.url !== '/request' && req.url !== '/stream')) return reply(res,404,{error:'not found'});
    const input = JSON.parse((await readBody(req)).toString('utf8'));
    if (req.url === '/stream') return await h2Stream(input, res);
    return reply(res,200,await h2Post(input));
  } catch (error) { return reply(res,500,{error:error instanceof Error ? error.message : String(error)}); }
});
server.listen(0,'127.0.0.1',() => console.log(`NEURA_H2_READY ${server.address().port}`));
function shutdown(){ server.close(() => process.exit(0)); setTimeout(() => process.exit(0),1500).unref(); }
process.once('SIGTERM',shutdown); process.once('SIGINT',shutdown);
if (Number.isInteger(parentPid) && parentPid > 0) setInterval(() => { try { process.kill(parentPid,0); } catch { shutdown(); } },2000).unref();
