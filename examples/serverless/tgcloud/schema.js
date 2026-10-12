import { table, integer } from 'sdk/db';

export const counters = table('counters', {
  chatId: integer('chat_id').primaryKey(),
  seen: integer('seen').notNull().default(0),
});
