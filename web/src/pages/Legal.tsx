// The privacy notice (/privacy) and how to delete an account
// (/delete-account): for anyone, signed in or out, as Google Play and the
// App Store ask of an app with accounts. The words follow what Taskiem does
// (docs/spec/taskiem-for-everyone.md §14, people-chatting.md §6 and §8,
// docs/legal/), and decision 0068's amendment: Taskiem is a way to run a
// business, beside its conversations. Counsel reviews them before production
// (docs/needs-people.md, PC1 and PC5).

import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { get } from "../api";
import { useLoad } from "../ui";

/** When the words below last changed, as the pages say it. */
export const LEGAL_UPDATED = "20 November 2026";

/** Where people write to Taskiem (TASKIEM_SUPPORT_EMAIL), when it is set. */
function useSupportEmail(): string | undefined {
  const { data } = useLoad(() => get<{ support_email?: string }>("/v1/app/config"), []);
  return data?.support_email || undefined;
}

/** How to reach Taskiem: the address when there is one, else from the app. */
function WriteToUs({ email }: { email?: string }) {
  return email ? (
    <a href={`mailto:${email}`} data-testid="legal-email">
      {email}
    </a>
  ) : (
    <>a message to @Taskiem in the app</>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h2>{title}</h2>
      {children}
    </section>
  );
}

/** /privacy: the privacy notice. */
export function PrivacyPage() {
  const email = useSupportEmail();
  return (
    <article className="login card legal" data-testid="privacy">
      <h1>Privacy notice</h1>
      <p className="hint">Last updated {LEGAL_UPDATED}</p>
      <p>
        Taskiem helps businesses run their work (orders, money, stock, staff and workflows) beside the conversations they have with customers and
        suppliers. This notice says what we keep about you, why, who sees it, and what you can do about it. It covers the Taskiem app for Android and
        iPhone, Taskiem on the web, and Taskiem on WhatsApp, Telegram and Slack.
      </p>

      <Section title="Who is responsible">
        <p>
          For your own account and your own side of Taskiem (your chats, your orders with businesses, your settings), Taskiem is responsible for your
          data.
        </p>
        <p>
          For what a business keeps about its customers, staff and work (its orders, invoices, customer records and its copy of its conversations),
          that business is responsible, and Taskiem keeps it for the business, on its instructions. To ask a business about what it keeps about you,
          ask that business. We help businesses answer those requests.
        </p>
        <p>
          To reach us about your data: <WriteToUs email={email} />.
        </p>
      </Section>

      <Section title="What we keep">
        <ul>
          <li>
            <b>Your account:</b> your name, phone number, photo if you add one, email address if you add one, and how you sign in (a code to your phone,
            a password, passkeys). You must be 18 or over to have an account.
          </li>
          <li>
            <b>Your conversations:</b> messages, photos, voice notes and files you send and receive in Taskiem, with businesses and with people.
          </li>
          <li>
            <b>Orders and money:</b> orders, bookings, invoices, receipts and payments between you and the businesses you deal with. Payments are made
            through each business's own payment provider; we never keep your card number.
          </li>
          <li>
            <b>A business's work,</b> if you run one or work in one: its products, stock, customers, staff, tasks, workflows and the money it records.
          </li>
          <li>
            <b>Saved addresses</b> you choose to keep for ordering.
          </li>
          <li>
            <b>Contacts, only if you agree:</b> to find which of your contacts are on Taskiem, the app sends their numbers in a scrambled (keyed hash)
            form. We use them to answer and do not keep them.
          </li>
          <li>
            <b>Where you are, only when you share it:</b> a place you send in a chat. We do not follow where you are.
          </li>
          <li>
            <b>Your phones:</b> each phone's notification address and an identifier for it, so notifications reach you and your account stays safe. We
            also keep when and from where (the internet address) you sign in, for your account's safety.
          </li>
          <li>
            <b>A record of important actions</b> (signing in, approving payments, changing roles), which a business and Taskiem keep to prevent fraud
            and settle disputes.
          </li>
        </ul>
        <p>
          We do not use advertising or analytics companies' tools, and we do not sell your data. The web uses one cookie, to keep you signed in.
        </p>
      </Section>

      <Section title="Why we use it">
        <ul>
          <li>To run Taskiem for you: your chats, orders, payments, notifications and a business's tools.</li>
          <li>To keep accounts and payments safe: signing in, confirming payments, spotting scams and abuse.</li>
          <li>
            To help with your work: in conversations with businesses, Taskiem reads messages to draft orders, translate and answer questions put to
            it. It does not do this in chats between people.
          </li>
          <li>To answer you when you write to us, and to meet the law.</li>
        </ul>
        <p>We never use your conversations for advertising, and we do not use them to train AI models.</p>
      </Section>

      <Section title="Who sees it">
        <ul>
          <li>
            <b>The businesses you chat with</b> see your name, phone number, your messages with them and your orders with them, since orders and
            deliveries need them. No business sees your other businesses, your chats with others or your orders elsewhere.
          </li>
          <li>
            <b>The people you chat with</b> see your name and photo, and your number only if you choose to show it.
          </li>
          <li>
            <b>Companies that run parts of Taskiem for us,</b> only for that work: our hosting and storage, Google's Firebase (to deliver
            notifications), Meta when you use Taskiem on WhatsApp, the services that send sign-in codes by text message and email, AI model providers
            (for the help described above), a speech-to-text service when a voice note is turned into words, and each business's own payment
            provider when you pay it.
          </li>
          <li>
            <b>The authorities,</b> only when the law requires it and through proper legal process. We give the least that is asked, and we publish how
            many such requests we receive on our <Link to="/transparency">transparency page</Link>.
          </li>
        </ul>
      </Section>

      <Section title="Chats between people">
        <p>
          We keep your chats with people on our servers so they show on every phone and computer you sign in on. Each person's copy is encrypted under
          a key of its own, but our servers can open them: chats on Taskiem are <b>not end-to-end encrypted</b>. No one at Taskiem reads them, except a
          moderator handling a report of them (the reported part only), a moderator reviewing a message our automated checks held as a likely scam, or
          when the law requires it. Each time is recorded.
        </p>
        <p>
          When someone writes to a person they have not chatted with before, automated checks look for known scams (asking for a code, promising a
          prize, asking for money). They keep only their decision, not the words. A held message is kept for up to 90 days for a moderator to review.
          A report is kept until it is reviewed and for one year after.
        </p>
      </Section>

      <Section title="How long we keep it">
        <ul>
          <li>Your account and your side of Taskiem: until you delete your account (see below).</li>
          <li>
            A business's records, including its copy of its conversations with you and your orders with it: as long as that business keeps them under
            its own rules and the law (for example, tax records).
          </li>
          <li>Records kept for safety and the law (the record of important actions, reports, payments): for the time the law or our safety needs.</li>
        </ul>
      </Section>

      <Section title="Keeping it safe">
        <p>
          Everything between your phone or browser and Taskiem travels encrypted. Messages, phone numbers and other personal details are encrypted
          where we keep them, each business's data is kept apart from every other business's, and people at Taskiem reach personal data only when
          their work needs it, with a record each time.
        </p>
      </Section>

      <Section title="Your choices and rights">
        <p>Under Nigeria's Data Protection Act 2023 and similar laws, you can:</p>
        <ul>
          <li>see the data we keep about you, and have a copy;</li>
          <li>have it corrected;</li>
          <li>
            have it deleted: delete your account at any time (<Link to="/delete-account">how to delete your account</Link>);
          </li>
          <li>object to how we use it, or ask us to limit it;</li>
          <li>take back a permission you gave (contacts, location, notifications) in your phone's settings or in Taskiem's;</li>
          <li>complain to the Nigeria Data Protection Commission.</li>
        </ul>
        <p>
          To use any of these, write to <WriteToUs email={email} />. We answer within 30 days.
        </p>
      </Section>

      <Section title="Data outside Nigeria">
        <p>
          Some of the companies that run parts of Taskiem for us work from outside Nigeria. When your data goes to them, we do so only with the
          protections the law requires.
        </p>
      </Section>

      <Section title="Changes to this notice">
        <p>
          When this notice changes in a way that matters, we tell you in the app before it applies. The date at the top says when it last changed.
        </p>
      </Section>

      <p className="legal-links">
        <Link to="/delete-account">How to delete your account</Link> · <Link to="/transparency">Requests from the authorities</Link> ·{" "}
        <Link to="/login">Back to Taskiem</Link>
      </p>
    </article>
  );
}

/** /delete-account: how to delete an account, and what is kept. */
export function DeleteAccountPage() {
  const email = useSupportEmail();
  return (
    <article className="login card legal" data-testid="delete-account">
      <h1>Delete your Taskiem account</h1>
      <p>
        You can delete your Taskiem account at any time, from the Taskiem app or on the web. Deleting it is permanent: you will need a new account to
        use Taskiem again.
      </p>

      <Section title="In the app">
        <ol data-testid="delete-steps-app">
          <li>Open Taskiem and go to You.</li>
          <li>Open Settings, then Delete your account, at the bottom.</li>
          <li>Confirm it is you, with a code sent to your phone, your password or a passkey.</li>
          <li>Press Delete my account.</li>
        </ol>
      </Section>

      <Section title="On the web">
        <ol data-testid="delete-steps-web">
          <li>
            <Link to="/login">Sign in to Taskiem</Link> with your phone number or email.
          </li>
          <li>
            Go to <Link to="/you/settings/delete">You › Settings › Delete your account</Link>.
          </li>
          <li>Confirm it is you, then press Delete my account.</li>
        </ol>
        <p>
          If you cannot sign in, write to <WriteToUs email={email} /> from the phone number or email on your account, and we will delete it for you.
        </p>
      </Section>

      <Section title="If you run or work in a business">
        <p>
          An account that owns a business, or has a role in one, cannot be deleted until that is handed over, because the business's records name you
          for what you did there. Make someone else the owner (the business's Members page), or ask its owner to remove you, then delete your account.
        </p>
      </Section>

      <Section title="What is deleted">
        <ul>
          <li>Your name, photo, phone number, email and the ways you signed in.</li>
          <li>Your copy of your chats, with businesses and with people, and the photos, voice notes and files in them.</li>
          <li>Your saved addresses, contacts' matches, settings and notification choices.</li>
          <li>Your sessions: you are signed out on every phone and computer at once.</li>
        </ul>
        <p>It happens at once. Each business you chatted with is told only that this customer left Taskiem.</p>
      </Section>

      <Section title="What is kept, and for how long">
        <ul>
          <li>
            <b>The businesses' own records</b> of what you did with them (their copy of your conversation, your orders, invoices and payments) stay
            with each business, as any shop keeps its receipts, under its own rules and the law. To have a business erase what it keeps about you, ask
            that business; we help it answer.
          </li>
          <li>
            <b>Records the law or safety requires:</b> payments and the record of important actions, for as long as the law requires; a report you made
            or that was made about you, for one year after it is reviewed, no longer linked to your account.
          </li>
          <li>
            <b>Messages you sent to other people</b> stay in their copy of the chat, as messages do in any chat app.
          </li>
        </ul>
      </Section>

      <p className="legal-links">
        <Link to="/privacy">Privacy notice</Link> · <Link to="/login">Back to Taskiem</Link>
      </p>
    </article>
  );
}
