Feature: The tgfake command serves a stand on a port
  A bot under test needs a Bot API to talk to and, when it is built on a
  language model, a model to ask. The command serves both on one port with no
  network and no token, so a test, a CI job or a person at the chat page can
  drive the bot exactly as Telegram would.

  Scenario: A bot talks to the stand the command serves
    Given the tgfake command is serving with its scripted model
    When a bot asks the Bot API who it is
    Then it learns it is @tgfake_bot
    When the person sends "hello" through the simulation API
    Then the bot's next getUpdates carries the message "hello"
    When the bot asks the model to answer "hello"
    Then the model answers "You said: hello"

  Scenario: A container bot registers an HTTP webhook with an explicit opt-in
    Given the tgfake command allows HTTP webhooks for container tests
    When the bot registers a webhook at "http://bot:8080/webhook"
    Then the webhook registration succeeds
