Feature: The person answers through the bot's reply keyboard
  A bot that installs a reply keyboard gets the label of a pressed button as an
  ordinary message from the person, not as a callback query. The keyboard
  stays until the bot removes it, whatever other messages the bot sends; a
  one-time keyboard hides after a press and can be opened again. ForceReply
  makes the person's next answer a reply to the message that asked for it.

  Background:
    Given an offline Telegram stand
    And the bot sent a reply keyboard "Photo, Video, Wallet"

  Scenario: A press is an ordinary message with the button's label
    When the person presses "Video"
    Then the bot receives the message "Video" from the person
    And the chat shows the reply keyboard

  Scenario: An inline keyboard leaves the reply keyboard in place
    When the bot sends a message with an inline keyboard
    Then the chat shows the reply keyboard

  Scenario: A one-time keyboard hides after a press and opens again
    Given the bot sent a one-time reply keyboard "Yes, No"
    When the person presses "Yes"
    Then the bot receives the message "Yes" from the person
    And the chat shows the reply keyboard hidden
    When the person presses "No"
    Then the bot receives the message "No" from the person

  Scenario: ForceReply makes the next press a reply to its message
    When the bot asks for a reply with ForceReply
    And the person presses "Wallet"
    Then the bot receives the message "Wallet" as a reply to that request
    And the chat no longer waits for a reply

  Scenario: Deleting the message that installed the keyboard takes it away
    When the bot deletes the message with the reply keyboard
    Then a press of the first button is refused
    And the bot receives no update

  Scenario: ReplyKeyboardRemove takes the keyboard away
    When the bot removes the reply keyboard
    Then a press of the first button is refused
    And the bot receives no update
