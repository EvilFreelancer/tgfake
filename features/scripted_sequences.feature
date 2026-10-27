Feature: The scripted model plays several roles, each in its own order
  A bot that calls a guard model and an agent model through one
  OpenAI-compatible endpoint gets each role's answers in that role's order. A
  named rule picks its requests by model, system prompt and offered tools, and
  walks its steps with one cursor per client. A step that calls a tool is
  answered by the result of exactly that call, and a scripted provider failure
  in between leaves the call waiting for its retry.

  Background:
    Given a scripted model with a guard role and an agent role

  Scenario: A guard call between agent turns does not move the agent on
    When client "a" asks the agent
    Then the model answers "Which style?"
    When client "a" asks the guard
    Then the model answers "allow"
    When client "a" asks the agent
    Then the model calls the tool "generate_image"

  Scenario: Each client walks the sequence on its own
    When client "a" asks the agent
    And client "a" asks the agent
    Then the model calls the tool "generate_image"
    When client "b" asks the agent
    Then the model answers "Which style?"

  Scenario: The result of the tool call gets the final answer
    When client "a" asks the agent
    And client "a" asks the agent
    And client "a" sends the result "ready" of that call
    Then the model answers "Image ready."

  Scenario: A provider failure while the model reads a tool result is retried
    Given the agent fails once with 503 after its tool call
    When client "a" asks the agent
    And client "a" asks the agent
    And client "a" sends the result "ready" of that call
    Then the model fails with 503 and Retry-After "2"
    When client "a" sends the result "ready" of that call
    Then the model answers "Image ready."

  Scenario: The journal lists every request and a reset rewinds the roles
    When client "a" asks the agent
    And client "a" asks the guard
    Then the journal lists the rules "agent, guard" for client "a"
    When the test resets the model
    And client "a" asks the agent
    Then the model answers "Which style?"
    And the journal lists the rules "agent" for client "a"
