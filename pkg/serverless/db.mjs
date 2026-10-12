import { DatabaseSync } from 'node:sqlite';

// Schema descriptions and SQL fragments stay independent of the connection.
const quote = name => '"' + String(name).replaceAll('"', '""') + '"';
const fragment = (text, params = [], ddlText = text) => ({ kind: 'sql', text, params, ddlText });
const scalar = value => {
  if (value === undefined || value === null) return null;
  if (typeof value === 'boolean') return Number(value);
  if (Object.prototype.toString.call(value) === '[object Date]') return value.getTime();
  return value;
};
function expression(value) {
  if (value?._table) return fragment(quote(value._table.name));
  if (value?.kind === 'sql') return value;
  if (value?.kind === 'column') return fragment(`${quote(value.table.name)}.${quote(value.name)}`, [], quote(value.name));
  if (value?.kind === 'table') return fragment(quote(value.name));
  return fragment('?', [scalar(value)]);
}
function combine(parts, separator = ', ') {
  return fragment(parts.map(p => p.text).join(separator), parts.flatMap(p => p.params), parts.map(p => p.ddlText).join(separator));
}
function sql(strings, ...values) {
  const parts = [fragment(strings[0])];
  values.forEach((value, i) => parts.push(expression(value), fragment(strings[i + 1])));
  return combine(parts, '');
}
sql.raw = text => fragment(text);
function column(type, name, options = {}) {
  return {
    kind: 'column', type, name, mode: options.mode, nullable: true,
    primaryKey(options = {}) { this.primary = true; this.auto = !!options.autoIncrement; return this; },
    notNull() { this.nullable = false; return this; },
    unique() { this.isUnique = true; return this; },
    default(value) { this.defaultValue = value; this.hasDefault = true; return this; },
    generatedAlwaysAs(value, options = {}) { this.generated = value; this.generatedMode = options.mode ?? 'virtual'; return this; },
    deprecated(reason) { this.deprecation = reason || 'deprecated'; return this; },
    references() { throw new Error('Foreign keys are not supported'); },
  };
}
const text = (name, options) => column('TEXT', name, options);
const integer = (name, options) => column('INTEGER', name, options);
const real = (name, options) => column('REAL', name, options);
const numeric = (name, options) => column('NUMERIC', name, options);
const blob = (name, options = {}) => column('BLOB', name, { mode: 'bytes', ...options });
const boolean = name => integer(name, { mode: 'boolean' });
const json = name => text(name, { mode: 'json' });
function table(name, columns, extras) {
  // Metadata is kept off the column namespace: columns may be called name/type.
  const result = { ...columns };
  const meta = { kind: 'table', name, columns, extras: [] };
  Object.defineProperty(result, '_table', { value: meta });
  for (const [key, col] of Object.entries(columns)) {
    col.name ??= key;
    col.key = key;
    col.table = meta;
  }
  meta.extras = Object.values(extras?.(result) ?? {});
  Object.defineProperties(result, {
    strict: { value() { meta.strict = true; return result; } },
    withoutRowid: { value() { meta.withoutRowid = true; return result; } },
    deprecated: { value(reason) { meta.deprecation = reason || 'deprecated'; return result; } },
  });
  return result;
}
function constraint(kind, name) {
  return { kind, name, on(...columns) { this.columns = columns; return this; },
    where(value) { this.filter = value; return this; } };
}
const index = name => constraint('index', name);
const uniqueIndex = name => constraint('uniqueIndex', name);
const unique = name => constraint('unique', name);
const check = (name, value) => ({ kind: 'check', name, value });
const foreignKey = () => { throw new Error('Foreign keys are not supported'); };
const binary = op => (a, b) => {
  if (a?.kind === 'column' && b?.kind !== 'column' && b?.kind !== 'sql') b = encode(a, b);
  const f = combine([expression(a), fragment(op), expression(b)], ' ');
  return fragment('(' + f.text + ')', f.params);
};
const junction = op => (...values) => {
  const f = combine(values.filter(v => v !== undefined).map(expression), ` ${op} `);
  return fragment('(' + (f.text || (op === 'AND' ? '1' : '0')) + ')', f.params);
};
const and = junction('AND'), or = junction('OR');
const unary = suffix => a => { const f = expression(a); return fragment(`(${f.text} ${suffix})`, f.params); };
const not = a => { const f = expression(a); return fragment(`NOT (${f.text})`, f.params); };
const range = op => (a, low, high) => {
  const f = combine([expression(a), fragment(op), expression(low), fragment('AND'), expression(high)], ' ');
  return fragment('(' + f.text + ')', f.params);
};
const arrayOp = op => (col, values) => {
  if (!values.length) return fragment(op === 'IN' ? '0' : '1');
  const a = expression(col), b = combine(values.map(v => expression(encode(col, v))));
  return fragment(`(${a.text} ${op} (${b.text}))`, [...a.params, ...b.params]);
};
const aggregate = name => value => {
  const f = value === undefined ? fragment('*') : expression(value);
  return fragment(`${name}(${f.text})`, f.params);
};
const direction = name => value => { const f = expression(value); return fragment(`${f.text} ${name}`, f.params); };
function encode(col, value) {
  if (value == null || value?.kind === 'sql') return value;
  switch (col.mode) {
    case 'json': return col.type === 'BLOB' ? new TextEncoder().encode(JSON.stringify(value)) : JSON.stringify(value);
    case 'boolean': return Number(value);
    case 'timestamp': return Math.floor(value.getTime() / 1000);
    case 'timestamp_ms': return value.getTime();
    default: return value;
  }
}
function decodeColumn(col, value) {
  if (value == null) return value;
  switch (col.mode) {
    case 'json': return JSON.parse(typeof value === 'string' ? value : new TextDecoder().decode(value));
    case 'boolean': return !!value;
    case 'timestamp': return new Date(value * 1000);
    case 'timestamp_ms': return new Date(value);
    default: return value;
  }
}
const dbExports = { table, text, integer, real, float: real, numeric, blob, boolean, json,
  index, uniqueIndex, unique, check, foreignKey, sql, and, or, not,
  eq: binary('='), ne: binary('<>'), gt: binary('>'), gte: binary('>='), lt: binary('<'), lte: binary('<='),
  like: binary('LIKE'), notLike: binary('NOT LIKE'), isNull: unary('IS NULL'), isNotNull: unary('IS NOT NULL'),
  between: range('BETWEEN'), notBetween: range('NOT BETWEEN'), inArray: arrayOp('IN'), notInArray: arrayOp('NOT IN'),
  count: aggregate('count'), sum: aggregate('sum'), avg: aggregate('avg'), min: aggregate('min'), max: aggregate('max'),
  asc: direction('ASC'), desc: direction('DESC') };

function makeDatabase(connection) {
  function execute(query, mode, named) {
    const f = typeof query === 'string' ? fragment(query) : query;
    const stmt = connection.prepare(f.text);
    const params = named ? [Object.fromEntries(Object.entries(named).map(([k, v]) => [k, scalar(v)]))] : f.params.map(scalar);
    if (mode === 'values') stmt.setReturnArrays(true);
    if (mode === 'all' || mode === 'values') return stmt.all(...params);
    if (mode === 'get') return stmt.get(...params) ?? null;
    let rows = [];
    if (stmt.columns().length) rows = stmt.all(...params);
    else {
      const result = stmt.run(...params);
      return { rowsAffected: Number(result.changes), lastInsertRowid: Number(result.lastInsertRowid), rows };
    }
    const info = connection.prepare('SELECT changes() AS changes, last_insert_rowid() AS id').get();
    return { rowsAffected: info.changes, lastInsertRowid: info.id, rows };
  }
  class Query {
    constructor(kind, target, projection) { this.kind = kind; this.target = target; this.projection = projection; this.orders = []; this.groups = []; }
    from(target) { this.target = target; return this; }
    where(...conditions) { this.filter = and(...conditions); return this; }
    orderBy(...values) { this.orders = values; return this; }
    groupBy(...values) { this.groups = values; return this; }
    having(...values) { this.havingFilter = and(...values); return this; }
    limit(value) { this.limitValue = value; return this; }
    offset(value) { this.offsetValue = value; return this; }
    distinct() { this.isDistinct = true; return this; }
    set(value) { this.assignments = value; return this; }
    returning(projection) { this.hasReturning = true; this.projection = projection; return this; }
    onConflictDoUpdate(value) { this.conflict = value; return this; }
    onConflictDoNothing(value = {}) { this.conflict = { ...value, nothing: true }; return this; }
    selection() {
      const columns = this.projection ?? this.target._table.columns;
      return combine(Object.entries(columns).map(([key, value]) => {
        const f = expression(value); return fragment(`${f.text} AS ${quote(key)}`, f.params);
      }));
    }
    assignmentsSQL(assignments) {
      return combine(Object.entries(assignments).map(([key, value]) => {
        const col = this.target._table.columns[key];
        if (!col) throw new Error(`Unknown column ${key}`);
        const f = expression(encode(col, value));
        return fragment(`${quote(col.name)} = ${f.text}`, f.params);
      }));
    }
    compile() {
      const meta = this.target?._table;
      if (!meta) throw new Error('Query requires a table');
      let parts;
      if (this.kind === 'select') parts = [fragment('SELECT ' + (this.isDistinct ? 'DISTINCT ' : '')), this.selection(), fragment(` FROM ${quote(meta.name)}`)];
      else if (this.kind === 'insert') {
        const rows = this.insertRows;
        if (!rows?.length) throw new Error('Insert requires values');
        const keys = [...new Set(rows.flatMap(row => Object.keys(row)))];
        const columns = keys.map(key => { if (!meta.columns[key]) throw new Error(`Unknown column ${key}`); return quote(meta.columns[key].name); });
        if (!keys.length && rows.length === 1) parts = [fragment(`INSERT INTO ${quote(meta.name)} DEFAULT VALUES`)];
        else {
          const tuples = rows.map(row => {
            const f = combine(keys.map(key => {
              const col = meta.columns[key];
              const value = key in row ? row[key] : col.hasDefault ? col.defaultValue : null;
              return expression(encode(col, value));
            }));
            return fragment('(' + f.text + ')', f.params);
          });
          parts = [fragment(`INSERT INTO ${quote(meta.name)} (${columns.join(', ')}) VALUES `), combine(tuples)];
        }
        if (this.conflict) {
          const targets = this.conflict.target ? [].concat(this.conflict.target).map(col => quote(col.name)) : [];
          parts.push(fragment(' ON CONFLICT' + (targets.length ? ' (' + targets.join(', ') + ')' : '')));
          if (this.conflict.nothing) parts.push(fragment(' DO NOTHING'));
          else parts.push(fragment(' DO UPDATE SET '), this.assignmentsSQL(this.conflict.set));
        }
      } else if (this.kind === 'update') parts = [fragment(`UPDATE ${quote(meta.name)} SET `), this.assignmentsSQL(this.assignments)];
      else parts = [fragment(`DELETE FROM ${quote(meta.name)}`)];
      if (this.filter) parts.push(fragment(' WHERE '), this.filter);
      if (this.groups.length) parts.push(fragment(' GROUP BY '), combine(this.groups.map(expression)));
      if (this.havingFilter) parts.push(fragment(' HAVING '), this.havingFilter);
      if (this.orders.length) parts.push(fragment(' ORDER BY '), combine(this.orders.map(expression)));
      if (this.limitValue !== undefined || this.offsetValue !== undefined) {
        parts.push(fragment(' LIMIT ? OFFSET ?', [this.limitValue ?? -1, this.offsetValue ?? 0]));
      }
      if (this.hasReturning) parts.push(fragment(' RETURNING '), this.selection());
      return combine(parts, '');
    }
    convert(rows) {
      const projection = this.projection ?? this.target._table.columns;
      return rows.map(row => Object.fromEntries(Object.entries(row).map(([key, value]) =>
        [key, projection[key]?.kind === 'column' ? decodeColumn(projection[key], value) : value])));
    }
    async all() { return this.convert(execute(this.compile(), 'all')); }
    async get() { return (await this.all())[0] ?? null; }
    values(value) {
      if (this.kind === 'insert') { this.insertRows = [].concat(value); return this; }
      return this.all().then(rows => rows.map(Object.values));
    }
    async run() {
      const result = execute(this.compile(), 'run');
      return this.hasReturning ? this.convert(result.rows) : result;
    }
  }
  return {
    select: projection => new Query('select', null, projection),
    insert: target => new Query('insert', target), update: target => new Query('update', target), delete: target => new Query('delete', target),
    async $count(target, filter) { const q = new Query('select', target, { n: dbExports.count() }); if (filter) q.where(filter); return (await q.get()).n; },
    async run(query, params) { return execute(query, 'run', params); },
    async all(query, params) { return execute(query, 'all', params); },
    async get(query, params) { return execute(query, 'get', params); },
    async values(query, params) { return execute(query, 'values', params); },
  };
}

// DDL cannot bind values. Render only SDK-generated fragments and quote literals.
function literal(value) {
  if (value === null) return 'NULL';
  if (typeof value === 'number' || typeof value === 'bigint') return String(value);
  if (ArrayBuffer.isView(value)) return "X'" + Buffer.from(value).toString('hex') + "'";
  return "'" + String(value).replaceAll("'", "''") + "'";
}
function ddl(value) {
  if (value?.kind !== 'sql') return literal(scalar(value));
  if (value.params.length) throw new Error('Bound values in schema SQL are unsupported; use sql.raw for reviewed DDL');
  return value.ddlText;
}
function columnDefinition(col) {
  let result = `${quote(col.name)} ${col.type}`;
  if (col.primary) result += ' PRIMARY KEY' + (col.auto ? ' AUTOINCREMENT' : '');
  if (!col.nullable) result += ' NOT NULL';
  if (col.isUnique) result += ' UNIQUE';
  if (col.hasDefault) result += ' DEFAULT ' + ddl(encode(col, col.defaultValue));
  if (col.generated) result += ` GENERATED ALWAYS AS (${ddl(col.generated)}) ${col.generatedMode.toUpperCase()}`;
  return result;
}
function migrate(connection, namespace) {
  connection.exec('BEGIN IMMEDIATE');
  try {
    for (const target of Object.values(namespace)) {
      const meta = target?._table;
      if (!meta) continue;
      if (meta.deprecation || Object.values(meta.columns).some(col => col.deprecation)) throw new Error('Deprecated schema objects require a manual migration');
      const existing = connection.prepare(`PRAGMA table_xinfo(${quote(meta.name)})`).all();
      const definitions = Object.values(meta.columns).map(columnDefinition);
      const tableConstraints = [];
      for (const extra of meta.extras) {
        if (extra.kind === 'unique') tableConstraints.push(`CONSTRAINT ${quote(extra.name)} UNIQUE (${extra.columns.map(col => quote(col.name)).join(', ')})`);
        if (extra.kind === 'check') tableConstraints.push(`CONSTRAINT ${quote(extra.name)} CHECK (${ddl(extra.value)})`);
      }
      if (existing.length) {
        const original = connection.prepare("SELECT sql FROM sqlite_master WHERE type='table' AND name=?").get(meta.name).sql;
        const flags = connection.prepare('PRAGMA table_list').all().find(row => row.name === meta.name && row.schema === 'main');
        if (!!flags.strict !== !!meta.strict || !!flags.wr !== !!meta.withoutRowid || tableConstraints.some(value => !original.includes(value))) {
          throw new Error(`Table ${meta.name} constraints changed; apply a reviewed manual migration`);
        }
        for (const col of Object.values(meta.columns)) {
          const old = existing.find(item => item.name === col.name);
          if (!old || old.type.toUpperCase() !== col.type || !!old.pk !== !!col.primary || !!old.notnull !== (!col.nullable || (!!meta.withoutRowid && !!col.primary)) ||
              (old.dflt_value ?? null) !== (col.hasDefault ? ddl(encode(col, col.defaultValue)) : null) || !!old.hidden !== !!col.generated ||
              ((col.generated || col.isUnique || col.auto) && !original.includes(columnDefinition(col)))) {
            throw new Error(`Table ${meta.name} changed; apply a reviewed manual migration`);
          }
        }
      } else {
        const options = [meta.strict && 'STRICT', meta.withoutRowid && 'WITHOUT ROWID'].filter(Boolean);
        connection.exec(`CREATE TABLE ${quote(meta.name)} (${[...definitions, ...tableConstraints].join(', ')}) ${options.join(', ')}`);
      }
      for (const extra of meta.extras) {
        if (extra.kind !== 'index' && extra.kind !== 'uniqueIndex') continue;
        const columns = extra.columns.map(col => col.kind === 'column' ? quote(col.name) : ddl(col)).join(', ');
        const statement = `CREATE ${extra.kind === 'uniqueIndex' ? 'UNIQUE ' : ''}INDEX ${quote(extra.name)} ON ${quote(meta.name)} (${columns})` + (extra.filter ? ' WHERE ' + ddl(extra.filter) : '');
        const original = connection.prepare("SELECT sql FROM sqlite_master WHERE type='index' AND name=?").get(extra.name);
        if (original && original.sql !== statement) throw new Error(`Index ${extra.name} changed; apply a reviewed manual migration`);
        if (!original) connection.exec(statement);
      }
    }
    connection.exec('COMMIT');
  } catch (error) { connection.exec('ROLLBACK'); throw error; }
}
