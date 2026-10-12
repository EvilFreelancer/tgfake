Feature: A person sends the bot a photo or a document
  A person can send a bot a photo or a document, with a caption and as part
  of an album. The update carries it the way Telegram delivers it - the
  photo's sizes from the smallest to the full one, the document's name and
  type, the caption, the album's media group - and the bot downloads the
  bytes with getFile and the file address, as on api.telegram.org.

  Scenario: A captioned photo is downloaded at its full size
    Given an offline Telegram stand
    When the person sends a 200x100 photo with the caption "what is on it?"
    Then the bot receives a photo with the caption "what is on it?" in 2 sizes, the last 200x100
    When the bot asks getFile for the last size
    Then the file at that path holds the bytes the person sent

  Scenario: A document is downloaded as it was sent
    Given an offline Telegram stand
    When the person sends the document "report.pdf"
    Then the bot receives the document "report.pdf" of type "application/pdf"
    When the bot asks getFile for the document
    Then the file at that path holds the bytes the person sent

  Scenario: An album arrives as messages of one media group
    Given an offline Telegram stand
    When the person sends an album of 2 photos with the caption "compare them"
    Then the bot receives 2 photos of one media group
    And only the first of them carries the caption "compare them"
