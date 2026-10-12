Feature: Local Telegram Serverless projects
  A bot runs its tgcloud handlers against the stand in Docker and shares
  a SQLite database with its authenticated Mini App endpoints.

  Scenario: Message handlers remember previous invocations
    Given a local serverless counter project with its schema applied
    When its message handler receives two messages
    Then the bot answers with a count of two
    And the Mini App endpoint sees the same count
