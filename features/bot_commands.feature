Feature: The offline Telegram stand keeps commands per scope and language
  A bot registers one list of commands per scope and per user language. A user
  is shown the first list the Bot API's chain reaches for them: a narrower
  scope before a wider one, and the language's own list before the one
  registered with no language.

  Scenario: A chat's own commands win over the bot's default ones
    Given an offline Telegram stand
    When the bot registers a default command list
    And the bot registers an English command list for one private chat
    Then that private chat shows the English list
    And another private chat shows the default list

  Scenario: The language's own list wins over the one registered without one
    Given an offline Telegram stand
    When the bot registers a default command list
    And the bot registers a Russian list and an English list for one private chat
    Then that private chat in Russian shows the Russian list
    And that private chat in English shows the English list

  Scenario: Deleting a chat's commands lets the default ones through
    Given an offline Telegram stand
    When the bot registers a default command list
    And the bot registers an English command list for one private chat
    And the bot deletes that private chat's commands
    Then that private chat shows the default list

  Scenario: An administrator's commands show only to administrators
    Given an offline Telegram stand
    When a test configures a group with the default user as owner
    And the bot registers a default command list
    And the bot registers a command list for that group's administrators
    Then an administrator of that group shows that list
    And a plain member of that group shows the default list
