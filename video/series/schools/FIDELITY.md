# How the Schools footage differs from the real app

The footage is the phone app's own screens, drawn by its test runner, with Bright Hill
Academy's data (`mobile/test/school_fixture.dart` and the films' own set-ups). It is
**not** a recording of a live server. Read this before publishing; if a caption or a line
says more than the app shows, trim it.

## For every film
- The server is a stand-in: its answers are made up in the story's names, amounts and
  dates. No real money moves, no real student, parent or phone data is shown. All names
  are invented. Account numbers, references and phone numbers are invented.
- Light theme only. The phone clock follows `films.json` (set per film, see below).
- The phone, status bar, keyboards and the tap ring are drawn by the compositor, not
  the app.
- A "cut" (Mrs Fashola's phone, Mallam Audu's phone, Mr Ibe's phone) is a separate
  set-up, not one live run.
- Positioning: Taskiem runs the school's work beside the chats the school already has.
  The films and lines never say it replaces WhatsApp.

## Where the series is reshaped, because the phone app lacks a step
- **Episode 1 is "Set up your school's people"**, not "Open your school". Turning
  Schools **on** is done on the web (the film starts with it already on); the narration
  says Schools is on and never that the phone turns it on. The **Bursar** is a custom
  role that Taskiem adds when an owner turns Schools on (on a plan with custom roles);
  **Gate** is a built-in role (`docs/schools.md`). The film shows School settings and Add
  staff with Bursar and Gate.
- **Episode 4: no WAEC pass-through and no family discounts** are filmed (the phone app
  has neither). Only class fees, required against optional, and Copy from the term
  before.
- **Episode 6: no QR receipt is filmed.** The line "a transfer lands on the child by
  itself" is **narrated only**; the film shows Tobi's account number and Send to the
  family, not a transfer arriving.
- **Episode 7: no claim that reminders skip a family that is on track** (not filmed, not
  said). **Mrs Lawal's request for a payment plan comes from the parents' web family
  page**, which is not filmed: on the phone it is a stand-in request already waiting
  under Chasing.
- **Episode 8: no teacher class list is filmed** (the phone app has none).
- **Episode 10: no audit log and no "reveal" control** are filmed. Names show for people
  whose role holds the permission, and are hidden for those who do not: shown with two
  phones (Funmi's, then Mr Ibe's), not a button. **Copying fees is per class** (the
  film copies one class's fees to the new term's First term; each class is copied
  separately).

## Per film
Phone clocks: school-open 13:00, school-classes-terms 13:00, school-students 09:30,
school-fees 09:30, school-invoice-term 13:15, school-get-paid 13:15, school-debtors 22:11,
school-gate 22:27, school-overview 07:05, school-safe-next-term 10:30.

**school-open** (Ep 1): see the reshaping above. Joining by WhatsApp is not an app
screen: "Waiting to join" and Mallam Audu's phone opening on the gate screen only are
shown as the app draws them; the invitation messages are not shown.
**school-classes-terms** (Ep 2): the term's dates are picked in the app's own date
picker; "418 students enrolled" is the stand-in's figure.
**school-students** (Ep 3): the CSV picker is a stand-in that returns a list of 399
students; the report and figures are made up. The guardian is chosen from the app's
Saved list.
**school-fees** (Ep 4): tuition ₦85,000 and the ₦102,000 total are the story's; the
fees are copied from "Third term" (last term) for **JSS 1A only**.
**school-invoice-term** (Ep 5): the plan (396 of 400) is the stand-in's; invoices are
not shown arriving on a family's phone.
**school-get-paid** (Ep 6): the account number is invented (the real one is issued by the
payment provider). The ₦60,000 cash is over the stand-in's owner amount (₦20,000 in the
settings); the second person is Mrs Fashola on a cut to her phone; the chat card clears
when she approves, and "a receipt goes out" is narrated, not shown.
**school-debtors** (Ep 7): the figures (families, ₦34,000 three-part plan, ₦51,000 two-part
plan) are the stand-in's. Mr Okafor's reply with a day is the story: the reply itself is
not shown. The reminder wording is the app's own rungs; no SMS arrives on screen.
**school-gate** (Ep 8): Mallam Audu's phone is a cut. The names, class and "SEE THE
BURSAR" result are the stand-in's. No amount is ever shown on the gate screen, which is
the app's own rule.
**school-overview** (Ep 9): **trimmed to 36.4 s**: the last seconds scroll the briefing
with nothing to scroll, so the end is cut shortly after the last caption begins. Two
captions at the start and the last two are merged in `captions/school-overview.json` so
that none lasts under 2 s. The close "does not reconcile" and every figure on the School
tab and in the briefing are the stand-in's. The morning summary on WhatsApp is **not**
filmed.
**school-safe-next-term** (Ep 10): the two phones are two set-ups with different roles;
"Dami stays in Primary 4" and the promotion result are the stand-in's. Two captions are
merged in `captions/school-safe-next-term.json` (a 1.4 s slot). "Third term" is the
previous term the fees are copied from.
**school-invoice-term** also has a merged caption (`captions/school-invoice-term.json`, a
1.8 s slot).

## Episode 0
The reel is ten stretches (about 12 s each, 124 s in all) of the films above, each with
its own caption. Taps and keyboards in each stretch are kept; each stretch inherits the
differences listed for its film.
