import vm from 'node:vm';
import path from 'node:path';

const config = await configuration;
const logs = [];
let logBytes = 0;
const consoleObject = Object.fromEntries(['log', 'debug', 'info', 'warn', 'error', 'trace'].map(level => [level, (...args) => {
  const text = util.format(...args);
  logBytes += Buffer.byteLength(text);
  if (logBytes > 1024 * 1024) throw new Error('Console output exceeds 1 MiB');
  logs.push({ level, text });
}]));
let connection;
try {
  connection = new DatabaseSync(config.database);
  connection.exec('PRAGMA foreign_keys = OFF; PRAGMA busy_timeout = 5000;');
  const db = makeDatabase(connection);
  const sdk = { db, api, fetch: sdkFetch, InputFile, FormData:SDKFormData, BotApiError, EndpointError };
  const context = vm.createContext({ console: consoleObject, TextEncoder, TextDecoder, URL, URLSearchParams });
  const modules = new Map();
  function getModule(name) {
    if (modules.has(name)) return modules.get(name);
    let module;
    const exports = name === 'sdk' ? sdk : name === 'sdk/db' ? dbExports : name === 'sdk/api' ? { api, InputFile, BotApiError } : name === 'sdk/fetch' ? { fetch: sdkFetch, InputFile, FormData:SDKFormData } : null;
    if (exports) {
      module = new vm.SyntheticModule(Object.keys(exports), function() {
        for (const [key, value] of Object.entries(exports)) this.setExport(key, value);
      }, { context, identifier: name });
    } else {
      if (!Object.hasOwn(config.modules, name)) throw new Error(`Module ${name} is not available`);
      module = new vm.SourceTextModule(config.modules[name], { context, identifier: name });
    }
    modules.set(name, module);
    return module;
  }
  const entry = getModule(config.module);
  await entry.link((specifier, parent) => {
    let name = specifier;
    if (specifier.startsWith('.')) {
      if (!specifier.endsWith('.js')) throw new Error('Relative imports require a .js extension');
      name = path.posix.normalize(path.posix.join(path.posix.dirname(parent.identifier), specifier)).slice(0, -3);
      if (name.startsWith('../') || name.startsWith('/')) throw new Error('Import escapes tgcloud/');
    }
    return getModule(name);
  });
  await entry.evaluate();
  let value = null;
  if (config.migrate) migrate(connection, entry.namespace);
  else {
    if (typeof entry.namespace.default !== 'function') throw new Error('Module must export a default function');
    // Payload objects belong to the invocation's realm as on the platform.
    context.inputJSON = JSON.stringify(config.input);
    context.contextJSON = JSON.stringify(config.context);
    const args = vm.runInContext('[JSON.parse(inputJSON), JSON.parse(contextJSON)]', context);
    value = await entry.namespace.default(...args);
  }
  process.stdout.write(JSON.stringify({ type: 'result', value: value ?? null, logs }) + '\n');
} catch (error) {
  const type = error instanceof EndpointError ? 'ENDPOINT_ERROR' : 'INTERNAL_ERROR';
  process.stdout.write(JSON.stringify({ type: 'result', value: null, logs, error: {
    type, description: error.message ?? String(error), parameters: error instanceof EndpointError ? error.parameters : undefined,
  } }) + '\n');
} finally {
  connection?.close();
  lines.close();
  process.stdin.destroy();
}
