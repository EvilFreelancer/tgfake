import { api } from 'sdk';

export default async function(query) {
  await api.answerInlineQuery({
    inline_query_id: query.id,
    results: [{
      type: 'article', id: 'echo', title: query.query || 'Hello',
      input_message_content: { message_text: query.query || 'Hello from Serverless!' },
    }],
    cache_time: 0,
  });
}
