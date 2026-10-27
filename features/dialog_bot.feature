Feature: A deterministic dialog bot integration
  A test bot can exercise its conversation over HTTP without a model or token.

  Scenario: Reply menu, independent guard, agent tools and ForceReply
    Given a self-contained HTTP bot test without credentials
    When the person selects a mode and completes a guarded tool dialog
    Then the transcript contains the reply menu, clarification and a reply to the result
