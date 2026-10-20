Feature: The offline Telegram stand carries a bot's payments
  The bot sees an invoice as its own message, the person's tap on Pay as a
  pre_checkout_query carrying the invoice's own price and payload, and the
  charge as a service message once the bot confirms.

  Scenario: A bot takes a payment in Telegram Stars
    Given an offline Telegram stand
    When the bot sends an invoice for 150 Stars with payload order-7
    Then the chat shows an invoice for 150 Stars
    When the person pays it
    Then the pre-checkout query carries 150 Stars and payload order-7
    When the bot confirms the pre-checkout
    Then the chat shows a successful payment of 150 Stars for order-7

  Scenario: A bot that refuses a pre-checkout takes no money
    Given an offline Telegram stand
    When the bot sends an invoice for 150 Stars with payload order-7
    And the person pays it
    And the bot refuses the pre-checkout
    Then the chat shows no payment

  Scenario: A returned charge reaches the bot as a service message
    Given an offline Telegram stand
    When the bot sends an invoice for 150 Stars with payload order-7
    And the person pays it
    And the bot confirms the pre-checkout
    And the charge is returned
    Then the chat shows a refund of 150 Stars for order-7

  Scenario: An invoice keyboard leads with the Pay button
    Given an offline Telegram stand
    When the bot sends an invoice with a Pay button and a cancel button
    Then the invoice keeps both buttons
