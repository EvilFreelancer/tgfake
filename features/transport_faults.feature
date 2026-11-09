Feature: Telegram fails one chat or one download at a time
  A scheduled fault can be scoped to one chat or to file downloads, and can
  lose the response after the method has been applied, the way a request that
  timed out on the bot's side may still have reached the chat. The outbox
  shows what the fault did to each call.

  Background:
    Given an offline Telegram stand

  Scenario: A fault scoped to one chat leaves the other chats alone
    When a test makes sendMessage to chat 111 fail with 403
    Then the bot's message to chat 222 is sent
    And the bot's message to chat 111 fails with 403

  Scenario: The response is lost after the message reached the chat
    When a test makes the next sendMessage to chat 111 lose its response after it applies
    Then the bot's message to chat 111 gets no response
    And chat 111 shows the message
    And the outbox marks the call applied with no response sent

  Scenario: A download fails while the Bot API keeps working
    Given the bot has sent a document
    When a test makes the next file download disconnect
    Then the bot's download of the document fails
    And the bot's message to chat 222 is sent
    And the bot's next download of the document succeeds
