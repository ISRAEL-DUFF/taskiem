# Answer your customers on WhatsApp and Telegram

Your assistant answers your customers on your own WhatsApp number or Telegram bot, day and night. It speaks their language: English, Pidgin, Yoruba, Hausa or Igbo. It hears voice notes and answers by voice. It answers only from what your business has told it, every reply is checked before it is sent, and a person takes over whenever a customer asks or the assistant is unsure.

This guide is for owners, and for field agents setting it up for a business. Setting it up takes about an hour, most of it in Meta's or Telegram's screens.

## 1. Choose where customers write

**Telegram** is the quickest: a bot is ready in two minutes.
1. In Telegram, open a chat with **@BotFather** and send `/newbot`.
2. Give it your business's name, then a username ending in `bot` (for example `mamaputkitchen_bot`).
3. BotFather sends a token: a long line of numbers and letters. Treat it like a password.
4. In Taskiem, open **Connections**, choose **Telegram**, and paste the token. That is all: Taskiem tells Telegram where to send messages when your assistant goes live.

Customers find your bot by its username, or a link `t.me/<username>` you share.

**WhatsApp** uses your own business number through Meta's WhatsApp Business Platform.

> **Before you start: the WhatsApp Business app.** A number connected this way usually cannot stay on the WhatsApp Business app on your phone at the same time. Meta allows both on some numbers ("coexistence"), but not all. If your staff answer customers from the app on a phone today, they will lose that number there. Many businesses use a second number for the assistant. Decide this before you connect.

1. In Meta's App Dashboard, make a Business app with **WhatsApp**, linked to your WhatsApp Business Account.
2. Add your business number and register it. Note its **Phone number ID** (a long number, not the phone number).
3. In Business Settings, make a **system user** for the app and give it a token with WhatsApp messaging. (A test token stops working after a day.)
4. Copy the **App secret** from the app's basic settings.
5. In Taskiem, open **Connections**, choose **WhatsApp**, and enter the token, the Phone number ID, the App secret, and a **Verify token** you make up (any word).

The last step is done after you choose your assistant (step 3 below): Taskiem shows you the address to paste into Meta, and where.

## 2. Tell it about your business

Open **What your assistant knows** and add your approved sources: your menu or price list, your opening hours, delivery areas, your policies. A web page, a document, a Google Sheet or a few lines typed in all work. Your assistant answers only from these: if something is not there, it says someone from the team will get back to the customer, and tells you.

Keep prices and offers here. They are checked: a price, a link or a promise your sources do not state is never sent.

## 3. Choose your assistant

Open **Templates**, then **Assistant**, and choose **Your assistant on WhatsApp** or **Your assistant on Telegram**. Fill in:
- **Business name**: how the assistant names your business.
- **About your business**: a few lines it always keeps in mind (what you sell, where you are, how you like to speak to customers).
- **Answer a voice note with a voice note**: on by default.
- **Only answer customers on your list**: leave it off to answer everyone. Turn it on to answer only the customers in your **Allowed customers** table (their WhatsApp number, written however you like: `2348031234567`, `+2348031234567` or `08031234567`; or their Telegram chat). Anyone else gets no answer at all, not even to STOP or PERSON.

Taskiem makes two tables for you: **Conversations** (what was said, so it remembers) and **Allowed customers**.

Then the workflow's readiness lists anything left to do, under **Before it can run** (it cannot answer until these are done) or **Worth doing**:
- for WhatsApp, the **callback address** to paste in Meta's App Dashboard (WhatsApp, then Configuration), with the same Verify token, and **messages** ticked;
- **Voice in workflows** turned on in Settings, if your assistant hears or speaks;
- your connection, if it is not made yet.

## 4. Try it before customers do

Open **Conversations** and use **Talk to it here**. Write as a customer would, in any of the five languages. Try asking for a price, for a person, and writing STOP. Your assistant answers as it will for customers, but sends nothing to anyone and changes nothing. Each try uses a little of your monthly allowance.

If an answer is wrong, add what was missing to **What your assistant knows**, or change **About your business**, and try again.

## 5. Go live

Publish the workflow. From then on, every message to your number or bot is answered.

## What customers are told

Customers must know they are talking to an assistant, and how to reach a person. Taskiem does this for you:
- A new customer's first answer starts with a short notice: they are talking to your business's automatic assistant, they can write **PERSON** for a person, and **STOP** to stop messages.
- **PERSON** (or "agent", "talk to someone", in their language) hands them to your team: you are told, and the assistant stays quiet for them for a day.
- **STOP** stops all messages to them until they write **START**.

Also tell customers, in your privacy notice or on your page, that you use an automatic assistant, that conversations are kept for your conversation days (30 by default, in Settings), and that they can ask you to delete what they said. One erase, by their WhatsApp number or Telegram chat (ask whoever runs your Taskiem, or `POST /v1/pii/erase`), removes their conversations, voice notes, their row in your allowed customers, and everything your assistant kept about them.

## Answering about their own account

If your customers keep money with you (a wallet, savings) or owe you on invoices, choose **Your assistant on WhatsApp, with their accounts** (or on Telegram) instead. Your assistant then also answers "how much do I have?", "have you received my payment?" and "what did I spend this month?", for the person writing and no one else:
- Who they are comes from the number or chat they write from, looked up in your **Customers' accounts** table: their number (as WhatsApp gives it, `2348031234567`, or their Telegram chat), their wallet at iswallet or bank account linked through Mono, and, for Telegram, the phone number their invoices are under. Someone who is not in the table is told someone from the team will help.
- What they owe is read from your invoices under their phone number.
- The AI model only decides what they are asking about. It never chooses whose account is read, and nothing is ever paid or changed.

You need a payments service that can read a customer's wallet or account (iswallet, or Mono) connected, and **Money and invoices** turned on under **What your business uses**.

## Day to day

- **Conversations** shows every customer, what they said and what was answered, with numbers and emails hidden. Open a customer to answer them yourself: your assistant stays quiet for them while you do.
- You are told when a customer asks for a person, when the assistant hands one to you (it was unsure, or a reply was held back: it stays quiet for them for 6 hours), when the AI model could not answer (once a day while it lasts), and when a reply could not be sent.
- While a person answers a customer, or your assistant is paused, what they write is still kept in the conversation, so your assistant knows it when it answers them again.
- A voice note your assistant cannot listen to (voice off, this month's voice used up) gets a polite note asking them to write instead.
- **This month** on Conversations shows messages, answers, how fast, and how much of your monthly allowance for AI answers and voice is used. When an allowance runs out, customers are told someone will get back to them (and voice notes are asked to be written) until the month turns.
- **Pause your assistant for every customer**, under Settings, **Your assistant**, stops all answers at once; messages are still kept.
