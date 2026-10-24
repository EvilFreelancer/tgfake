Feature: The chat page opens links in Telegram's in-app browser
  Telegram opens a link a person taps in a chat, an address in a message or a
  url button, in a browser of its own rather than in a separate browser app.
  The site there is laid out at the width of the phone, under Telegram's
  address bar and with Telegram's toolbar floating over its bottom. The chat
  page plays that browser in a phone frame, so a site can be checked at the
  size a Telegram user sees it, with no phone.

  Scenario: Links from the bot open in the in-app browser
    Given the chat page of a stand and a site to open
    When the bot sends the person the site's address with a url button to it
    Then the chat shows the address as a link to the site
    And tapping the url button opens the site in the in-app browser
    And tapping the link in the message opens the site in the in-app browser

  Scenario: The person can open links in a new tab instead
    Given the chat page of a stand and a site to open
    When the bot sends the person the site's address with a url button to it
    And the person chooses to open links in a new tab
    Then tapping the url button opens the site in a new tab and not in the in-app browser

  Scenario: The site is laid out at the width of the phone
    Given the chat page of a stand and a site to open
    When the person opens the site in the in-app browser of a 440x956 phone
    Then the site is laid out 440 CSS pixels wide and 825 high
    And the address bar shows the host of the site

  Scenario: Only web addresses open
    Given the chat page of a stand and a site to open
    When the person opens javascript:alert(1) in the in-app browser
    Then the in-app browser stays closed and the chat says why
