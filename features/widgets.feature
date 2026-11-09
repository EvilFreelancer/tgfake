Feature: Bots send interactive HTML widgets
  The stand keeps widget code and state in messages, delivers widget queries
  and runs the widget on the chat page without network access.

  Scenario: A person asks a widget for fresh data
    Given an offline Telegram stand
    When the bot sends a widget with an initial counter
    And the person asks the widget to increment the counter
    Then the bot's next poll carries the widget query
    When the bot answers the widget query with the new counter
    And the bot changes the widget counter to 1
    Then the chat shows the widget counter 1

  Scenario: A shared widget keeps one public state across messages
    Given an offline Telegram stand
    When the bot sends a shared widget with a public price
    And the bot sends the same shared widget to another chat
    And the bot changes the shared price to 5.47
    Then both chats show the shared price 5.47
