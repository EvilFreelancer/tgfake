Feature: Ordinary bots and Mini Apps use the same offline Bot API
  The stand keeps files, inline results, Mini App messages and webhook delivery
  as state that a test can drive through its simulation API.

  Scenario: A bot downloads and reuses an uploaded document
    Given a stand with inline mode enabled
    When the bot uploads a document containing notes
    Then getFile gives a working Telegram download path
    And the bot can send the same file id again

  Scenario: A person chooses an inline result
    Given a stand with inline mode enabled
    When the person asks the inline bot for hello
    And the bot offers an article containing an inline answer
    And the person chooses that article
    Then the chat shows the inline answer from the person

  Scenario: A Mini App shares a prepared message
    Given a stand with inline mode enabled
    When the bot prepares an article for the default Mini App user
    And the person shares it into a private chat
    Then the chat shows the inline answer from the person

  Scenario: A webhook receives a user's message
    Given a stand with inline mode enabled
    When the bot registers a local webhook
    And the person sends a webhook message
    Then the webhook receives the message update

  Scenario: A bot reads configured chat membership
    Given a stand with inline mode enabled
    When a test configures a group with the default user as owner
    Then getChatMember reports that user as the owner
