// The rest of the app's settings (docs/spec/mobile-app.md §3.6), as the web
// has them under Account and Settings (web/src/pages/Account.tsx,
// Settings.tsx ChatToolsSwitch): who you are and your email (Add your email
// for an account made in a chat), the app lock, the chat tools switch for
// owners and admins, and the chats you have linked (WhatsApp by a code sent
// to it, with the approval PIN; Telegram by its one-time link; Slack by the
// Confirm sent there). What changes a way in asks for your authenticator
// code (or your password, when there is no stronger way); a passkey comes
// with M4.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../app/lock.dart';
import '../app/prefs.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';

/// The web's `.card`: the panel, its border, the radius; 20 px by 22.
class SettingsCard extends StatelessWidget {
  const SettingsCard({super.key, required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Semantics(
        container: true,
        // Its buttons and fields each their own, not one block read out.
        explicitChildNodes: true,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
          decoration: BoxDecoration(
            color: t.panel,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(Tk.radius),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Semantics(
                  header: true,
                  child: Text(title, style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text)),
                ),
              ),
              ...children,
            ],
          ),
        ),
      ),
    );
  }
}

/// Account.tsx's words for a failure.
String _say(Object e) => e is ApiError ? e.message : (e is Offline ? 'You’re offline. Try again when you’re back online.' : 'Something went wrong. Try again.');

String _fmt(DateTime d) {
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  final l = d.toLocal();
  return '${l.day} ${months[l.month - 1]} ${l.year}';
}

// --- the proof (passkeys.ts proof, Account.tsx ProofField) ---

/// The ways the person can prove it is them (me.factors).
({bool passkey, bool totp, bool password}) factorsOf(Auth auth) {
  final f = auth.me?.factors;
  bool has(String k) => f is Map && f[k] == true;
  return (passkey: has('passkey'), totp: has('totp'), password: has('password'));
}

/// What a change to a way in asks for here: an authenticator code; a
/// password when there is no stronger way; nothing when there is none;
/// null when only a passkey would do (M4).
String? proofAsked(Auth auth) {
  final f = factorsOf(auth);
  if (f.totp) return 'totp';
  if (f.passkey) return null;
  if (f.password) return 'password';
  return '';
}

FactorProof proofOf(Auth auth, String typed) => switch (proofAsked(auth)) {
  'totp' => FactorProof(totp: typed),
  'password' => FactorProof(password: typed),
  _ => const FactorProof(),
};

/// ProofField: the box for the code or the password, to [to]; the words
/// saying it needs a passkey when nothing else would do.
class ProofField extends StatelessWidget {
  const ProofField({super.key, required this.auth, required this.to, required this.controller});
  final Auth auth;
  final String to;
  final TextEditingController controller;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return switch (proofAsked(auth)) {
      'totp' => Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: TkField(label: 'Authenticator code, to $to', controller: controller, digits: 6, keyboard: TextInputType.number),
      ),
      'password' => Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: TkField(label: 'Your password, to $to', controller: controller, obscure: true),
      ),
      null => Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Text('This needs your passkey. Do it on the web, where Taskiem asks for it.', style: tkHint(t)),
      ),
      _ => const SizedBox.shrink(),
    };
  }
}

/// A section that loads, acts, and says what went wrong.
abstract class _Section<W extends StatefulWidget> extends State<W> {
  bool busy = false;
  String? error;

  Future<void> run(Future<void> Function() f, {String? done}) async {
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await f();
      if (done != null && mounted) showTkToast(context, done);
    } on Object catch (e) {
      error = _say(e);
    }
    if (mounted) setState(() => busy = false);
  }
}

// --- you ---

/// Who is signed in, and for an account made in a chat, Add your email.
class YouCard extends StatefulWidget {
  const YouCard({super.key, required this.auth});
  final Auth auth;

  @override
  State<YouCard> createState() => _YouCardState();
}

class _YouCardState extends _Section<YouCard> {
  final _email = TextEditingController();

  @override
  void dispose() {
    _email.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final u = widget.auth.me?.user;
    final placeholder = u?.emailPlaceholder == true;
    return SettingsCard(
      title: 'You',
      children: [
        if ((u?.name ?? '').isNotEmpty) Text(u!.name!, style: tkText(size: 15, weight: 600, color: t.text)),
        if (!placeholder && (u?.email ?? '').isNotEmpty) Text(u!.email!, style: tkHint(t)),
        if (placeholder) ...[
          const SizedBox(height: 8),
          Text('Add your email', style: tkText(size: 14, weight: 600, color: t.text)),
          const SizedBox(height: 4),
          Text(
            'Your account was made in a chat, so Taskiem cannot email you yet. Add an email to get receipts and invoices, and to sign in with it.',
            style: tkHint(t),
          ),
          const SizedBox(height: 12),
          TkField(label: 'Email', controller: _email, keyboard: TextInputType.emailAddress, autofill: const [AutofillHints.email]),
          if (error != null) TkErrorBox(error!),
          const SizedBox(height: 12),
          TkButton(
            label: 'Send the link',
            primary: true,
            minHeight: 44,
            onPressed: busy
                ? null
                : () => run(() async {
                    await widget.auth.api.addEmail(body: AddEmailRequest(email: _email.text.trim()));
                    _email.clear();
                  }, done: 'A link is on its way: open it to confirm'),
          ),
        ],
      ],
    );
  }
}

// --- the lock ---

/// The app lock, as WhatsApp's: off until turned on, then locking at once,
/// after a minute or after half an hour away. Every change is made with the
/// phone's own unlock (a phone left on a counter cannot be made to open
/// freely, and turning it on proves the phone has a lock to ask for).
class LockCard extends StatefulWidget {
  const LockCard({super.key, required this.prefs, this.unlocker});
  final Prefs prefs;
  final Unlocker? unlocker;

  @override
  State<LockCard> createState() => _LockCardState();
}

class _LockCardState extends State<LockCard> {
  String? _error;

  Future<void> _choose(Duration? after) async {
    final prefs = widget.prefs;
    if (after == prefs.lockAfter) return;
    final u = widget.unlocker;
    if (u != null) {
      final has = await u.available();
      if (!has && after != null) {
        if (mounted) setState(() => _error = 'Set a screen lock on this phone first: a fingerprint, face or PIN.');
        return;
      }
      if (has && !await stepOut(u.unlock)) return;
    }
    if (mounted) setState(() => _error = null);
    await prefs.setLockAfter(after);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final prefs = widget.prefs;
    Widget row(String label, Duration? after) {
      final on = prefs.lockAfter == after;
      return TkChoiceRow(
        control: TkRadio(checked: on),
        label: Text(label, style: tkText(size: 14, color: t.text)),
        onTap: () => _choose(after),
      );
    }

    return ListenableBuilder(
      listenable: prefs,
      builder: (context, _) => SettingsCard(
        title: 'App lock',
        children: [
          Text('Ask for this phone’s fingerprint, face or PIN to open Taskiem.', style: tkHint(t)),
          const SizedBox(height: 12),
          row('Off', null),
          for (final d in lockTimes) row(lockTimeLabel(d), d),
          if (_error != null) TkErrorBox(_error!),
          Text(
            prefs.lock
                ? 'When Taskiem opens, and when you come back to it ${prefs.lockAfter == Duration.zero ? 'from another app' : 'after ${lockTimeLabel(prefs.lockAfter!).substring(6)} away'}. Picking a photo or a file does not lock it.'
                : 'Taskiem opens without asking.',
            style: tkHint(t),
          ),
        ],
      ),
    );
  }
}

// --- chat tools ---

/// Settings.tsx ChatToolsSwitch: owners and admins turn chat tools on for
/// everyone.
class ChatToolsCard extends StatefulWidget {
  const ChatToolsCard({super.key, required this.auth, this.onChanged});
  final Auth auth;
  final ValueChanged<bool>? onChanged;

  @override
  State<ChatToolsCard> createState() => _ChatToolsCardState();
}

class _ChatToolsCardState extends _Section<ChatToolsCard> {
  bool? _on;

  @override
  void initState() {
    super.initState();
    widget.auth.api.getChatTools().then(
      (v) {
        if (mounted) setState(() => _on = v.chatTools);
      },
      onError: (Object e) {
        if (mounted) setState(() => error = _say(e));
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final on = _on ?? false;
    return SettingsCard(
      title: 'Chat tools',
      children: [
        Semantics(
          toggled: on,
          enabled: _on != null && !busy,
          child: TkChoiceRow(
            radio: false,
            control: TkCheckbox(checked: on),
            label: Text('Name a business to message it', style: tkText(size: 14, color: t.text)),
            onTap: () {
              if (_on == null || busy) return;
              final next = !on;
              // What was just chosen shows at once; taken back if refused.
              setState(() => _on = next);
              run(() async {
                try {
                  await widget.auth.api.putChatTools(body: PutChatToolsRequest(chatTools: next));
                  widget.onChanged?.call(next);
                } on Object {
                  _on = !next;
                  rethrow;
                }
              }, done: 'Saved');
            },
          ),
        ),
        Text(
          'On WhatsApp, Telegram, Slack and in your Taskiem chat, a message naming linked businesses goes straight to them. Write @Taskiem to ask Taskiem instead. Everyone in the business gets the same.',
          style: tkHint(t),
        ),
        if (error != null) TkErrorBox(error!),
      ],
    );
  }
}

// --- your chats ---

/// "Resting": a chat quiet too long acts for no one until linked again.
Widget _dormant(TkTokens t, String what) =>
    Text('Resting: this $what has been quiet a long time, so it acts for no one until you link it again.', style: tkHint(t));

/// Account.tsx WhatsApp: the number linked by a code sent to it, and its
/// approval PIN.
class WhatsAppCard extends StatefulWidget {
  const WhatsAppCard({super.key, required this.auth});
  final Auth auth;

  @override
  State<WhatsAppCard> createState() => _WhatsAppCardState();
}

class _WhatsAppCardState extends _Section<WhatsAppCard> {
  WhatsAppBinding? _b;
  bool _sent = false;
  final _number = TextEditingController();
  final _code = TextEditingController();
  final _proof = TextEditingController();
  final _pin = TextEditingController();
  final _pinProof = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    for (final c in [_number, _code, _proof, _pin, _pinProof]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final b = await widget.auth.api.getWhatsApp();
      if (mounted) setState(() => _b = b);
    } on Object catch (e) {
      if (mounted) setState(() => error = _say(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _b;
    if (b == null || !b.enabled) return error == null ? const SizedBox.shrink() : SettingsCard(title: 'WhatsApp', children: [TkErrorBox(error!)]);
    final api = widget.auth.api;
    final number = b.number is String ? b.number as String : null;
    final pending = b.pending;
    return SettingsCard(
      title: 'WhatsApp',
      children: [
        Text(
          'Link your WhatsApp number to approve requests, check what ran and start workflows by chat${b.platformNumber != null ? ' with Taskiem’s number ${b.platformNumber}' : ''}. Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.',
          style: tkHint(t),
        ),
        const SizedBox(height: 12),
        if (number != null) ...[
          Text(
            'Linked: $number${b.verifiedAt is String ? ' · since ${_fmt(DateTime.parse(b.verifiedAt as String))}' : ''}',
            style: tkText(size: 14, weight: 600, color: t.text),
          ),
          if (b.dormant == true) _dormant(t, 'number'),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            children: [
              TkPlainButton(
                label: 'Unlink',
                danger: true,
                onTap: busy
                    ? null
                    : () async {
                        final placeholder = RegExp(r'^owner\+.*@phone\.taskiem\.invalid$').hasMatch(widget.auth.me?.user?.email ?? '');
                        final yes = await showTkConfirm(
                          context,
                          title: 'Unlink this number?',
                          body: placeholder
                              ? 'Your account was made in a chat: its sign-in name comes from this number and is released with it. Add your email first, so you can still sign in.'
                              : null,
                          ok: 'Unlink',
                          danger: true,
                        );
                        if (yes) {
                          await run(() async {
                            await api.unbindWhatsApp();
                            await _load();
                          }, done: 'WhatsApp unlinked');
                        }
                      },
              ),
            ],
          ),
          const SizedBox(height: 16),
        ],
        TkField(label: number != null ? 'Link another number instead' : 'Your WhatsApp number', controller: _number, keyboard: TextInputType.phone),
        Padding(
          padding: const EdgeInsets.only(top: 6, bottom: 12),
          child: Text('With the country code, e.g. +2348012345678', style: tkHint(t)),
        ),
        ProofField(auth: widget.auth, to: 'link a number', controller: _proof),
        Align(
          alignment: Alignment.centerLeft,
          child: TkPlainButton(
            label: 'Send a code',
            onTap: busy || proofAsked(widget.auth) == null
                ? null
                : () => run(() async {
                    final p = proofOf(widget.auth, _proof.text);
                    await api.startWhatsAppBinding(
                      body: WhatsAppBindRequest(number: _number.text.trim(), totp: p.totp, password: p.password),
                    );
                    _proof.clear();
                    _sent = true;
                    await _load();
                  }, done: 'Code sent'),
          ),
        ),
        if (pending != null || _sent) ...[
          const SizedBox(height: 16),
          TkField(
            label: 'Code sent to ${pending?.number ?? 'your number'}',
            controller: _code,
            digits: 6,
            keyboard: TextInputType.number,
            autofill: const [AutofillHints.oneTimeCode],
          ),
          Padding(
            padding: const EdgeInsets.only(top: 6, bottom: 12),
            child: Text('Or send the code back to Taskiem in WhatsApp. It works for 10 minutes.', style: tkHint(t)),
          ),
          ListenableBuilder(
            listenable: _code,
            builder: (context, _) => Align(
              alignment: Alignment.centerLeft,
              child: TkPlainButton(
                label: 'Link',
                primary: true,
                onTap: busy || _code.text.length != 6
                    ? null
                    : () => run(() async {
                        await api.verifyWhatsAppBinding(body: WhatsAppVerifyRequest(code: _code.text));
                        _code.clear();
                        _number.clear();
                        _sent = false;
                        await _load();
                      }, done: 'WhatsApp linked'),
              ),
            ),
          ),
        ],
        if (error != null) TkErrorBox(error!),
        if (number != null && b.flows) _pinPart(t, b.pin),
      ],
    );
  }

  /// Account.tsx WhatsAppPin.
  Widget _pinPart(TkTokens t, WhatsAppPin pin) {
    final api = widget.auth.api;
    final f = factorsOf(widget.auth);
    final strong = f.passkey || f.totp;
    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            header: true,
            child: Text('Approval PIN', style: tkText(size: 15, weight: 600, color: t.text)),
          ),
          const SizedBox(height: 4),
          Text(
            'Six digits you enter in WhatsApp to confirm approvals whose policy allows a WhatsApp PIN. Policies asking for a passkey or authenticator code still send you here. Five wrong PINs lock it for 15 minutes.',
            style: tkHint(t),
          ),
          const SizedBox(height: 12),
          if (pin.set) ...[
            Text(
              'PIN set${pin.setAt != null ? ' ${_fmt(pin.setAt!)}' : ''}${pin.lockedUntil != null ? ' · locked until ${_fmt(pin.lockedUntil!)}' : ''}',
              style: tkText(size: 14, color: t.text),
            ),
            const SizedBox(height: 8),
            Align(
              alignment: Alignment.centerLeft,
              child: TkPlainButton(
                label: 'Remove',
                danger: true,
                onTap: busy
                    ? null
                    : () async {
                        final yes = await showTkConfirm(context, title: 'Remove your WhatsApp PIN?', ok: 'Remove', danger: true);
                        if (yes) {
                          await run(() async {
                            await api.removeWhatsAppPin();
                            await _load();
                          }, done: 'PIN removed');
                        }
                      },
              ),
            ),
            const SizedBox(height: 12),
          ],
          if (!strong)
            Text('Add a passkey or an authenticator first: the PIN stands in for them in WhatsApp.', style: tkHint(t))
          else ...[
            TkField(label: pin.set ? 'New PIN' : 'PIN', controller: _pin, digits: 6, obscure: true, keyboard: TextInputType.number),
            Padding(
              padding: const EdgeInsets.only(top: 6, bottom: 12),
              child: Text('Not one digit repeated, nor a run like 123456', style: tkHint(t)),
            ),
            ProofField(auth: widget.auth, to: 'set the PIN', controller: _pinProof),
            ListenableBuilder(
              listenable: _pin,
              builder: (context, _) => Align(
                alignment: Alignment.centerLeft,
                child: TkPlainButton(
                  label: pin.set ? 'Change PIN' : 'Set PIN',
                  onTap: busy || _pin.text.length != 6 || proofAsked(widget.auth) == null
                      ? null
                      : () => run(() async {
                          final p = proofOf(widget.auth, _pinProof.text);
                          await api.setWhatsAppPin(
                            body: WhatsAppPinRequest(pin: _pin.text, totp: p.totp, password: p.password),
                          );
                          _pin.clear();
                          _pinProof.clear();
                          await _load();
                        }, done: 'PIN saved'),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Account.tsx Telegram: a one-time t.me link opens the bot, and the
/// Telegram account that opens it is linked.
class TelegramCard extends StatefulWidget {
  const TelegramCard({super.key, required this.auth});
  final Auth auth;

  @override
  State<TelegramCard> createState() => _TelegramCardState();
}

class _TelegramCardState extends _Section<TelegramCard> {
  TelegramLink? _b;
  Uri? _link;
  final _proof = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _proof.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final b = await widget.auth.api.getTelegram();
      if (mounted) setState(() => _b = b);
    } on Object catch (e) {
      if (mounted) setState(() => error = _say(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _b;
    if (b == null || !b.enabled) return error == null ? const SizedBox.shrink() : SettingsCard(title: 'Telegram', children: [TkErrorBox(error!)]);
    final api = widget.auth.api;
    final link = _link;
    return SettingsCard(
      title: 'Telegram',
      children: [
        Text(
          'Link your Telegram account to approve requests, check what ran and start workflows by chat${b.botUsername != null ? ' with @${b.botUsername}' : ''}. Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.',
          style: tkHint(t),
        ),
        const SizedBox(height: 12),
        if (b.linked) ...[
          Text(
            'Linked: ${b.username != null ? '@${b.username}' : 'your Telegram account'}${b.verifiedAt != null ? ' · since ${_fmt(b.verifiedAt!)}' : ''}',
            style: tkText(size: 14, weight: 600, color: t.text),
          ),
          if (b.dormant == true) _dormant(t, 'account'),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerLeft,
            child: TkPlainButton(
              label: 'Unlink',
              danger: true,
              onTap: busy
                  ? null
                  : () async {
                      final placeholder = RegExp(r'^owner\+.*@telegram\.taskiem\.invalid$').hasMatch(widget.auth.me?.user?.email ?? '');
                      final yes = await showTkConfirm(
                        context,
                        title: 'Unlink Telegram?',
                        body: placeholder
                            ? 'Your account was made in a chat: its sign-in name comes from this Telegram account and is released with it. Add your email first, so you can still sign in.'
                            : null,
                        ok: 'Unlink',
                        danger: true,
                      );
                      if (yes) {
                        await run(() async {
                          await api.unlinkTelegram();
                          _link = null;
                          await _load();
                        }, done: 'Telegram unlinked');
                      }
                    },
            ),
          ),
          const SizedBox(height: 16),
        ],
        ProofField(auth: widget.auth, to: 'link Telegram', controller: _proof),
        Align(
          alignment: Alignment.centerLeft,
          child: TkPlainButton(
            label: b.dormant == true ? 'Link again' : (b.linked ? 'Link another account instead' : 'Link Telegram'),
            onTap: busy || proofAsked(widget.auth) == null
                ? null
                : () => run(() async {
                    final p = proofOf(widget.auth, _proof.text);
                    final r = await api.startTelegramLink(
                      body: FactorProof(totp: p.totp, password: p.password),
                    );
                    _proof.clear();
                    _link = Uri.tryParse(r.url);
                  }),
          ),
        ),
        if (link != null) ...[
          const SizedBox(height: 12),
          Text('Open Telegram and press Start. The link works once, for 10 minutes.', style: tkHint(t)),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            children: [
              TkPlainButton(label: 'Open Telegram', primary: true, onTap: () => openOutside(link)),
              TkPlainButton(
                label: 'Done',
                onTap: busy
                    ? null
                    : () => run(() async {
                        _link = null;
                        await _load();
                      }),
              ),
            ],
          ),
        ],
        if (error != null) TkErrorBox(error!),
      ],
    );
  }
}

/// Account.tsx Slack: Taskiem finds the person in the business's workspace
/// by email and sends a Confirm button there.
class SlackCard extends StatefulWidget {
  const SlackCard({super.key, required this.auth});
  final Auth auth;

  @override
  State<SlackCard> createState() => _SlackCardState();
}

class _SlackCardState extends _Section<SlackCard> {
  SlackLink? _b;
  final _proof = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _proof.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final b = await widget.auth.api.getSlack();
      if (mounted) setState(() => _b = b);
    } on Object catch (e) {
      if (mounted) setState(() => error = _say(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _b;
    if (b == null || !b.enabled) return error == null ? const SizedBox.shrink() : SettingsCard(title: 'Slack', children: [TkErrorBox(error!)]);
    final api = widget.auth.api;
    return SettingsCard(
      title: 'Slack',
      children: [
        Text(
          'Get the approvals you decide in this business in Slack, as a direct message from your business’s Slack app with Approve and Reject buttons. Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.',
          style: tkHint(t),
        ),
        const SizedBox(height: 12),
        if (b.linked) ...[
          Text('Linked in your workspace${b.verifiedAt != null ? ' · since ${_fmt(b.verifiedAt!)}' : ''}', style: tkText(size: 14, weight: 600, color: t.text)),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerLeft,
            child: TkPlainButton(
              label: 'Unlink',
              danger: true,
              onTap: busy
                  ? null
                  : () async {
                      if (await showTkConfirm(context, title: 'Unlink Slack?', ok: 'Unlink', danger: true)) {
                        await run(() async {
                          await api.unlinkSlack();
                          await _load();
                        }, done: 'Slack unlinked');
                      }
                    },
            ),
          ),
        ],
        if (b.pending) ...[
          Text('Open Slack and press Confirm in the message from your business’s app. Then come back here.', style: tkHint(t)),
          Align(
            alignment: Alignment.centerLeft,
            child: TkLink(label: 'I pressed it', onTap: _load),
          ),
          const SizedBox(height: 8),
        ],
        if (!b.linked) ...[
          ProofField(auth: widget.auth, to: 'link Slack', controller: _proof),
          Align(
            alignment: Alignment.centerLeft,
            child: TkPlainButton(
              label: b.pending ? 'Send the Confirm again' : 'Link Slack',
              onTap: busy || proofAsked(widget.auth) == null
                  ? null
                  : () => run(() async {
                      final p = proofOf(widget.auth, _proof.text);
                      await api.startSlackLink(
                        body: FactorProof(totp: p.totp, password: p.password),
                      );
                      _proof.clear();
                      await _load();
                    }),
            ),
          ),
        ],
        if (error != null) TkErrorBox(error!),
      ],
    );
  }
}
