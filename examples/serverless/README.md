# Serverless counter

A local Telegram Serverless project. Its message handler counts messages per chat in SQLite and replies through the fake Bot API. Its authenticated `getCounter` endpoint reads the same database for the Mini App user.

From the repository root:

```bash
docker pull node:24-bookworm-slim
go run ./cmd/tgfake --serverless examples/serverless --serverless-migrate
```

Open the chat page at `http://127.0.0.1:18790/` and send two messages. The answers show counts 1 and 2. Use `--serverless-db bot.sqlite` to retain the counter between runs. The default fake token is `123456:SERVERLESS`.

Add `--inline` to enable the Inline button and the included inline query handler. It offers an article echoing the query, which you can select into the chat. Incoming documents sent through `/sim/media` are downloaded with `api.getFileContent` and answered with their byte count; they do not increment the text counter. See the [file and inline examples](../../docs/serverless.md#testing-files-inline-bots-and-mini-apps).

From a Mini App served on an origin which also proxies `/api/`:

```javascript
Telegram.WebApp.Serverless.call('getCounter', {}, (error, counter) => {
  if (error) console.error(error.message);
  else console.log(counter.seen);
});
```

The [runtime reference](../../docs/serverless.md) describes Docker requirements, explicit schema creation and supported SDK behavior. The [CI reference](../../docs/ci.md#serverless-projects) shows the root action's inputs.
