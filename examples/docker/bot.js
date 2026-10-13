// A webhook echo bot for the Compose example. Uses Node's standard library.
const http = require('node:http');
const api = process.env.TELEGRAM_API;
const token = process.env.BOT_TOKEN;
const secret = process.env.WEBHOOK_SECRET;

const server = http.createServer(async (req, res) => {
  if (req.method !== 'POST' || req.url !== '/webhook') {
    res.writeHead(404).end();
    return;
  }
  if (req.headers['x-telegram-bot-api-secret-token'] !== secret) {
    res.writeHead(403).end();
    return;
  }
  try {
    let body = '';
    for await (const chunk of req) body += chunk;
    const update = JSON.parse(body);
    const response = await fetch(`${api}/bot${token}/sendMessage`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ chat_id: update.message.chat.id, text: `You said: ${update.message.text}` }),
    });
    if (!response.ok) throw new Error(`sendMessage returned ${response.status}`);
    res.writeHead(200).end();
  } catch (error) {
    console.error(error);
    res.writeHead(500).end();
  }
});
server.listen(8080, '0.0.0.0', async () => {
  try {
    const response = await fetch(`${api}/bot${token}/setWebhook`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: process.env.WEBHOOK_URL, secret_token: secret }),
    });
    const body = await response.json();
    if (!response.ok || !body.ok) throw new Error(`setWebhook failed: ${JSON.stringify(body)}`);
    console.log('Webhook registered');
  } catch (error) {
    console.error(error);
    server.close(() => process.exit(1));
  }
});
process.on('SIGTERM', () => server.close());
process.on('SIGINT', () => server.close());
