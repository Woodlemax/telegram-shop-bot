# Optional Telegram group or channel link

Each model can have an optional `telegram_url`. Existing products and the
add-product wizard default to no link; a community button appears only when
one is set. The button is below the description in the Mini App and is the
first button below the product text in the bot. It does not change the cart,
create an order or grant file access.

In the administrator bot chat, open `/editproduct ID` and press **Group /
channel link**, then send the link. **Remove Telegram link** clears it. The
input expires after 15 minutes; `/cancel`, the cancel button, another command
or another button leaves the previous link in place. Starting a link dialog
ends other active input dialogs. Interactive editing requires a private admin
chat; field commands follow the existing administrator authorization.

Direct commands:

```text
/editproduct 2 telegram https://t.me/your_channel
/editproduct 2 telegram @your_group
/editproduct 2 telegram https://t.me/+InvitationCode
/editproduct 2 telegram -
```

Public `https://t.me/name`, bare `t.me/name`, `@name`, private `t.me/+code`
and legacy `t.me/joinchat/code` invitations are supported. `telegram.me`
aliases normalize to `https://t.me`. Links must be HTTPS Telegram URLs;
external domains, credentials, ports, query strings and post URLs are rejected.
The administrator supplies the group/channel address; the server does not
request membership information or verify the target remotely.

Mini App uses Telegram's native link-opening function when available, with
a normal browser link as fallback. Missing links produce no empty button.
SQLite migration 028 adds the field without changing descriptions, prices,
stock, archives, orders or the saved exchange rate. SQL writes validate links;
bot/API rendering also hides invalid values written outside the admin UI.
