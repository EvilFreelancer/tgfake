import readline from 'node:readline';
import util from 'node:util';

const lines = readline.createInterface({ input: process.stdin });
let initialize;
const configuration = new Promise(resolve => { initialize = resolve; });
const pendingCalls = new Map();
let nextCall = 0;
lines.on('line', line => {
  const message = JSON.parse(line);
  if (initialize) { initialize(message); initialize = null; return; }
  const receive = pendingCalls.get(message.id);
  if (receive) receive(message);
});
class BotApiError extends Error {
  constructor(method, result) {
    super(result.description);
    this.name = 'BotApiError'; this.code = result.error_code; this.description = result.description;
    this.method = method; this.parameters = result.parameters;
  }
}
class EndpointError extends Error {
  constructor(description, parameters) { super(description); this.name = 'EndpointError'; this.parameters = parameters; }
}
class InputFile {
  constructor(bytes, filename, options = {}) {
    this.bytes = new Uint8Array(bytes); this.filename = filename; this.type = options.type;
  }
}
function serializeFiles(value, files, topKey) {
  if (value instanceof InputFile) {
    const key = topKey ?? 'upload_' + files.length;
    files.push({ key, name: value.filename, data: Buffer.from(value.bytes).toString('base64') });
    return 'attach://' + key;
  }
  if (Array.isArray(value)) return value.map(v => serializeFiles(v, files));
  if (value && typeof value === 'object' && Object.prototype.toString.call(value) !== '[object Date]' && !ArrayBuffer.isView(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, v]) => [key, serializeFiles(v, files)]));
  }
  return value;
}
async function callAPI(method, params = {}) {
  const id = ++nextCall, files = [], serialized = {};
  for (const [key, value] of Object.entries(params)) {
    if (value instanceof InputFile) serializeFiles(value, files, key);
    else serialized[key] = serializeFiles(value, files);
  }
  const answer = new Promise(resolve => pendingCalls.set(id, message => { pendingCalls.delete(id); resolve(message.body); }));
  process.stdout.write(JSON.stringify({ type: 'api', id, method, params: serialized, files }) + '\n');
  const result = await answer;
  if (!result.ok) throw new BotApiError(method, result);
  return result.result;
}
const api = new Proxy({ call: callAPI,
  async getFileStream(file_id) {
    const file = await callAPI('getFile',{file_id});
    const response = await sdkFetch(config.apiURL.replace(/\/$/,'')+'/file/bot'+config.token+'/'+file.file_path);
    if (!response.ok) throw new BotApiError('getFile',{error_code:response.status,description:response.statusText});
    async function* chunks() {
      let size=0;
      for await(const chunk of response.body) {
        size+=chunk.length;
        if(size>20*1024*1024) throw new Error('File download exceeds 20 MB');
        yield chunk;
      }
    }
    const body={ [Symbol.asyncIterator]:chunks, async bytes() {
      const values=[]; for await(const chunk of chunks()) values.push(chunk);
      return new Uint8Array(Buffer.concat(values));
    }};
    return {file,body};
  },
  async getFileContent(file_id) { return (await api.getFileStream(file_id)).body.bytes(); },
}, { get(target, name) {
  if (name === 'then') return undefined;
  return target[name] ?? (params => callAPI(name, params));
} });
class SDKFormData extends FormData {
  append(key, value, filename) {
    if (value instanceof InputFile) super.append(key, new Blob([value.bytes], {type:value.type}), filename ?? value.filename);
    else if (filename === undefined) super.append(key, value);
    else super.append(key, value, filename);
  }
  set(key, value, filename) {
    if (value instanceof InputFile) super.set(key, new Blob([value.bytes], {type:value.type}), filename ?? value.filename);
    else if (filename === undefined) super.set(key, value);
    else super.set(key, value, filename);
  }
}
async function sdkFetch(url, options = {}) {
  let { body, ...rest } = options;
  if (body instanceof InputFile) body = body.bytes;
  if (body?.sdkBody) { rest.headers=new Headers(rest.headers); rest.headers.set('Content-Type',body.type); body=body.value; }
  const request = new Request(url, { ...rest, body });
  const id = ++nextCall;
  const chunks = [];
  let ended = false, error, wake, consumed = false;
  let resolveHead, rejectHead;
  const head = new Promise((resolve,reject) => { resolveHead=resolve; rejectHead=reject; });
  pendingCalls.set(id, message => {
    if (message.type === 'fetchHead') resolveHead(message);
    if (message.type === 'fetchChunk') chunks.push(new Uint8Array(Buffer.from(message.data,'base64')));
    if (message.type === 'fetchEnd' || message.type === 'fetchError') {
      ended = true; pendingCalls.delete(id);
      if (message.type === 'fetchError') { error=new Error(message.error); rejectHead(error); }
    }
    wake?.(); wake=undefined;
  });
  process.stdout.write(JSON.stringify({type:'fetch',id,url:request.url,method:request.method,
    headers:Object.fromEntries(request.headers), body:Buffer.from(await request.arrayBuffer()).toString('base64')})+'\n');
  const response = await head;
  async function* stream() {
    if (consumed) throw new Error('Response body already consumed');
    consumed = true;
    while (true) {
      if (chunks.length) { yield chunks.shift(); continue; }
      if (error) throw error;
      if (ended) return;
      await new Promise(resolve => { wake=resolve; });
    }
  }
  const readText = async () => {
    const chunks = [];
    for await (const chunk of stream()) chunks.push(chunk);
    return Buffer.concat(chunks).toString('utf8');
  };
  return { status: response.status, statusText: response.statusText, ok: response.status >= 200 && response.status < 300, url: response.url,
    headers: new Headers(response.headers), body: { [Symbol.asyncIterator]: stream }, text: readText,
    json: async () => JSON.parse(await readText()) };
}
sdkFetch.body = {
  json: value => ({ sdkBody: true, type: 'application/json', value: JSON.stringify(value) }),
  text: value => ({ sdkBody: true, type: 'text/plain', value }),
  form: value => ({ sdkBody: true, type: 'application/x-www-form-urlencoded', value: new URLSearchParams(value).toString() }),
};
