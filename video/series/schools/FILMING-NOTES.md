# Filming the Schools series (Bright Hill Academy)

Read `video/series/FILM-GUIDE.md` first (environment, the Film API, the loop, the rules, and
**the founder's rule: every step must be reproducible by a viewer in the real app**), then
`video/series/schools/README.md` (the story, the episodes) and `mobile/CLAUDE.md`.

The School tab and its pages are phone-app screens: `mobile/lib/school/*.dart`. The app's own
tests for them: `mobile/test/school_test.dart` and `school_fixture.dart` (the stand-in server
`schoolServer`, `schoolClasses`, `schoolAmaka`, `schoolBriefing`, `schoolApprovalAlert`).
Copy their set-up. Other tests that show school parts: `grep -l -i school mobile/test/*.dart`.
Fashion/Food films (`mobile/test/film_*_test.dart`) show the Film patterns; copy
`film_food_*` for chat-based parts (briefing, approval card).

## Rules specific to Schools
- Films are named `school-<name>` (frames in `<out>/school-<name>/`), tests
  `mobile/test/film_school_<name>_test.dart`. Shared helper files need **unique names**
  (`film_bh_ep12_world.dart`, with your episode numbers). Do not edit existing films or
  the app's own test files; import or copy.
- **Only what the app does.** Before writing a film, read the screen's code and find the
  **real route** to it (which tab, which row, which button). Every screen must be reached
  by taps a person can make; never open a page by test code unless that is only how the film
  starts (the film may start on a screen the person opens before the video begins). If a
  step in the episode outline does not exist in the phone app (for example turning Schools
  on, roles, a family page), **do not film or fake it**: say so in your report and film the
  real parts. The README outline was written before the app was checked.
- Cuts to a second person's phone (a second approver, the gate phone) are fine.
- Captions: 8 to 12 words, present tense, no caption shorter than 2 seconds, first caption
  with `who:`; the phone clock set by `--time` when composing (report it).
- `flutter analyze` clean on your files. One real render at a time (background), with
  `flutter test --timeout 40m`; then compose a 9:16 preview and **look at stills** at the
  key moments (tap rings, keyboard, result) and check that each caption matches the frame.
- Report back: the files, the output folders, running time, captions with times, what each
  film shows, the real route to each screen, and every stand-in or difference.

## The story (use these numbers where a screen shows them)
**Bright Hill Academy**, Ikorodu, Lagos; First term 2026/2027. Proprietor and principal
**Mrs Tolulope Fashola**; bursar **Mr Chukwudi Ibe** (the person who does most of the work);
gateman **Mallam Audu**; parents **Mrs Bimpe Lawal** (child **Tobi Lawal**, JSS 1A) and
**Mr Segun Danjuma** (child **Dayo Danjuma**, Primary 4), cash. Use the app fixture's own
students (Amaka Obi, JSS1A/014) and classes (JSS 1A, JSS 1B, JSS 2A, Primary 4, SS 1B) where
they are already there. About 400 students. Fees, JSS 1A: Tuition ₦85,000, PTA levy
₦5,000, Books ₦12,000 (₦102,000); optional Transport ₦20,000. Term expected
₦42,800,000; collected ₦29,960,000 (70%). Tobi's invoice ₦102,000; Mr Danjuma's cash
payment large enough to need a second person (use the app's own threshold).

## Films (one or two per episode; the helper decides what the app lets us film)
| Episode | Films |
| --- | --- |
| 1 Open your school | `school-open` (only if the app has any of it: Schools on, the Bursar and Gate roles) |
| 2 Classes, sessions, terms | `school-classes-terms` (Setup: classes with arms, the session, First term, start it) |
| 3 Students and families | `school-students` (the list, import, add one student with a guardian from Saved) |
| 4 Set the fees | `school-fees` (class fees, amounts, required/optional, copy from the term before) |
| 5 Invoice the term | `school-invoice-term` (the plan first, then drafts or send) |
| 6 Get paid | `school-get-paid` (a student's account number, cash at the desk, a second person's Approve card) |
| 7 Chase what is owed | `school-debtors` (aged, the reminder rung, promise, a payment plan) |
| 8 Resumption day | `school-gate` (Clearance by class, an override, the gate screen on Mallam Audu's phone) |
| 9 Know your school | `school-overview` (the School tab, the day's close, the chat briefing) |
| 10 Keep it safe, next term | `school-safe-next-term` (masked names, promotion, start the next term) |
