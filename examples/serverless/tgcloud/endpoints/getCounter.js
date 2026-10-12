import { db } from 'sdk';
import { eq } from 'sdk/db';
import { counters } from '../schema.js';

export default async function(input, ctx) {
  const row = await db.select().from(counters).where(eq(counters.chatId, ctx.initData.user.id)).get();
  return { seen: row?.seen ?? 0 };
}
