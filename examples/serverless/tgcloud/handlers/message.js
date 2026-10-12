import { api, db } from 'sdk';
import { sql } from 'sdk/db';
import { counters } from '../schema.js';

export default async function(message, ctx) {
  if (message.document) {
    const content = await api.getFileContent(message.document.file_id);
    await api.sendMessage({ chat_id: message.chat.id, text: `Received ${content.length} bytes.` });
    return;
  }
  const [row] = await db.insert(counters)
    .values({ chatId: message.chat.id, seen: 1 })
    .onConflictDoUpdate({ target: counters.chatId, set: { seen: sql`${counters.seen} + 1` } })
    .returning().run();
  console.info('update', ctx.update.update_id);
  await api.sendMessage({ chat_id: message.chat.id, text: `Hello! I've seen ${row.seen} message(s) from you.` });
}
