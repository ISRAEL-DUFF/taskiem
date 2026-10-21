# Connecting your apps

A connection is your account with a service. Taskiem keeps it locked away and never shows it again.

- **Google Sheets and Gmail:** press **Connect with Google** and choose your Google account. That is all.
- **Paystack:** open your Paystack dashboard, go to *Settings*, then *API Keys & Webhooks*, and copy the **secret key**. Choose whether it is your live key (real money) or your test key (practice). Taskiem checks the key with Paystack before saving it, and tells you if you pasted the public key by mistake. You do not need to set up a webhook: Taskiem asks Paystack for new payments every few minutes.
- **SMS and other services:** each one shows where its key is and what it starts with.

Your first connection to a service is called **main**, and templates use it unless you choose another.

A connection shows which step needs it when something is missing: the draft's list says *"Paystack is not connected"* with a **Connect it** button.
