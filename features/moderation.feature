Feature: The stand moderates chat members the way Telegram does
  A bot under test bans, restricts, promotes and admits members against a
  supergroup whose membership, permissions and clock the stand keeps, and
  reads the effects back through getChatMember, getUpdates and refused
  incoming messages.

  Scenario: A restricted member is silenced until the restriction expires
    Given a supergroup the bot administers
    When the bot restricts member 42 from sending messages for an hour
    Then member 42 has the status "restricted"
    And a message from member 42 is refused
    When the stand's clock advances 2 hours
    Then a message from member 42 is delivered

  Scenario: Banning and unbanning a member
    Given a supergroup the bot administers
    When the bot bans member 42
    Then member 42 has the status "kicked"
    And a message from member 42 is refused
    When the bot unbans member 42
    Then member 42 has the status "left"

  Scenario: Promoting a member to an administrator
    Given a supergroup the bot administers
    When the bot promotes member 42 with the right to delete messages
    Then member 42 is an administrator with "can_delete_messages"

  Scenario: Approving a join request admits the member
    Given a supergroup the bot administers
    When user 7 asks to join the chat
    Then the bot's next poll carries the join request
    When the bot approves the join request of user 7
    Then member 7 has the status "member"

  Scenario: A member's arrival is a service message and a member update
    Given a supergroup the bot administers
    When user 9 joins the chat
    Then the bot's next poll carries the service message and the member update
    And member 9 has the status "member"
