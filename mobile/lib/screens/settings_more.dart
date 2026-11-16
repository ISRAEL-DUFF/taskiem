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
import '../kit/look.dart' show TkEyebrow, TkFace, TkTopCard, tkFigure;
import '../kit/forms.dart' show TkSwitch;
import '../kit/mobile.dart' show TkListRow, TkSection, tkInitials;
import '../kit/page.dart' show TkPageScreen;
import '../theme/look.dart';
import '../theme/theme.dart';

/// A settings page of its own (docs/spec/mobile-app-look.md, "Settings"):
/// the app's head with Back to Settings, its cards on the ground. A card
/// titled as the page shows no title of its own.
class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key, required this.title, required this.children, this.backLabel = 'Back to Settings'});
  final String title;
  final String backLabel;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => _PageTitle(
    title: title,
    child: TkPageScreen(title: title, titleSize: 26, backLabel: backLabel, children: children),
  );
}

class _PageTitle extends InheritedWidget {
  const _PageTitle({required this.title, required super.child});
  final String title;

  @override
  bool updateShouldNotify(_PageTitle old) => old.title != title;
}

/// A group of settings: its heading on the ground, then its card.
class SettingsCard extends StatelessWidget {
  const SettingsCard({super.key, required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final own = context.dependOnInheritedWidgetOfExactType<_PageTitle>()?.title == title;
    return Semantics(
      container: true,
      // Its buttons and fields each their own, not one block read out.
      explicitChildNodes: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (!own) SettingsHeading(title),
          Container(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
            decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < children.length; i++) ...[if (i > 0 && children[i] is! SizedBox && children[i - 1] is! SizedBox) const SizedBox(height: 6), children[i]],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// A group's heading, large on the ground (`.b-sec`).
class SettingsHeading extends StatelessWidget {
  const SettingsHeading(this.title, {super.key});
  final String title;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(4, 4, 4, 10),
    child: Semantics(
      header: true,
      child: Text(title, style: tkDisplay(size: 20, letterSpacing: -0.4, color: TkTheme.tokens(context).text)),
    ),
  );
}

/// What is set, large on the top card: its eyebrow, the figure, a line,
/// then its buttons.
class SettingsTop extends StatelessWidget {
  const SettingsTop({super.key, required this.eyebrow, this.trailing, required this.big, this.line, this.children = const []});
  final String eyebrow;
  final String? trailing;
  final String big;
  final String? line;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return TkTopCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(eyebrow, color: look.limeInk, trailing: trailing),
          const SizedBox(height: 6),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(big, maxLines: 1, style: tkFigure(context, size: 26)),
          ),
          if (line != null) Padding(padding: const EdgeInsets.only(top: 4), child: Text(line!, style: tkText(size: 13, lineHeight: 1.4, color: t.muted))),
          for (final c in children) ...[const SizedBox(height: 12), c],
        ],
      ),
    );
  }
}

/// Quiet words on the ground, under a card.
class SettingsNote extends StatelessWidget {
  const SettingsNote(this.words, {super.key});
  final String words;

  @override
  Widget build(BuildContext context) =>
      Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text(words, style: tkText(size: 13, lineHeight: 1.4, color: TkTheme.tokens(context).muted)));
}

/// A page's cards, 14 apart.
class SettingsColumn extends StatelessWidget {
  const SettingsColumn({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 14), children[i]],
    ],
  );
}

/// A button in a settings card: the app's own small one.
class _Btn extends StatelessWidget {
  const _Btn({required this.label, this.onTap, this.primary = false, this.danger = false});
  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final bool danger;

  @override
  Widget build(BuildContext context) => TkSmallButton(label: label, primary: primary, danger: danger, onPressed: onTap);
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
    final me = widget.auth.me;
    final name = (u?.name ?? '').isNotEmpty ? u!.name! : 'You';
    final roles = me?.roles is List ? [for (final r in me!.roles as List) '$r'] : const <String>[];
    final who = [
      if (roles.isNotEmpty) '${roles.first[0].toUpperCase()}${roles.first.substring(1).replaceAll('_', ' ')}',
      if ((me?.tenantName ?? '').isNotEmpty) me!.tenantName!,
    ].join(' · ');
    // You at the top of Settings, as drawn: your face, name, email, and
    // your role in the business; an account made in a chat adds its email.
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(22)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          MergeSemantics(
            child: Row(
              children: [
                TkFace(tkInitials(name), size: 56, colors: tkFaceFor(name)),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(name, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkDisplay(size: 19, letterSpacing: -0.3, color: t.text)),
                      if (!placeholder && (u?.email ?? '').isNotEmpty) Text(u!.email!, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted)),
                      if (who.isNotEmpty) Text(who, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted)),
                    ],
                  ),
                ),
              ],
            ),
          ),
          if (placeholder) ...[
            const SizedBox(height: 14),
            Text('Add your email', style: tkText(size: 15, weight: 600, color: t.text)),
            const SizedBox(height: 4),
            Text(
              'Your account was made in a chat, so Taskiem cannot email you yet. Add an email to get receipts and invoices, and to sign in with it.',
              style: tkHint(t),
            ),
            const SizedBox(height: 12),
            TkField(label: 'Email', controller: _email, keyboard: TextInputType.emailAddress, autofill: const [AutofillHints.email]),
            if (error != null) TkErrorBox(error!),
            const SizedBox(height: 12),
            TkSmallButton(
              label: 'Send the link',
              primary: true,
              onPressed: busy
                  ? null
                  : () => run(() async {
                      await widget.auth.api.addEmail(body: AddEmailRequest(email: _email.text.trim()));
                      _email.clear();
                    }, done: 'A link is on its way: open it to confirm'),
            ),
          ],
        ],
      ),
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

  void _flip() {
    if (_on == null || busy) return;
    final next = !(_on ?? false);
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
  }

  @override
  Widget build(BuildContext context) {
    final on = _on ?? false;
    // For everyone here, as drawn: the switch on its row; what it does
    // under the card.
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SettingsHeading('For everyone here'),
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'Chat tools',
              sub: 'Name a business to message it',
              icon: 'b-tools',
              square: TkLook.of(context).red,
              trailing: TkSwitch(label: 'Name a business to message it', on: on, onChanged: _on == null || busy ? null : (_) => _flip()),
              onTap: _on == null || busy ? null : _flip,
            ),
          ],
        ),
        if (error != null) ...[const SizedBox(height: 10), TkErrorBox(error!)],
        const SizedBox(height: 10),
        const SettingsNote(
          'On WhatsApp, Telegram, Slack and in your Taskiem chat, a message naming linked businesses goes straight to them. Write @Taskiem to ask Taskiem instead. Everyone in the business gets the same.',
        ),
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
  // Linking another number, and setting the PIN: each opened when asked for.
  bool _another = false;
  bool _pinOpen = false;
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
    final what =
        'Approve requests, check what ran and start workflows by chat${b.platformNumber != null ? ' with Taskiem’s number ${b.platformNumber}' : ''}.';
    // As drawn (docs/spec/mobile-app-look.md, "Settings"): the number
    // linked large on the top card with Unlink; linking, and the approval
    // PIN, each on a card of its own.
    return SettingsColumn(children: [
      if (number != null)
        SettingsTop(
          eyebrow: b.dormant == true ? 'Resting' : 'Linked',
          trailing: b.verifiedAt is String ? 'since ${_fmt(DateTime.parse(b.verifiedAt as String))}' : null,
          big: number,
          line: what,
          children: [
            if (b.dormant == true) _dormant(t, 'number'),
            TkSmallButton(
                label: 'Unlink',
                semantics: 'Unlink $number',
                onPressed: busy
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
      if (number != null) SettingsNote('Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.'),
      // Linked: another number only when asked for (as drawn).
      if (number != null && !_another && pending == null && !_sent)
        TkSection(
          inset: 56,
          children: [
            TkListRow(title: 'Link another number', icon: 'b-whatsapp', square: const Color(0xFF25D366), onTap: () => setState(() => _another = true)),
          ],
        )
      else
      SettingsCard(
      title: number != null ? 'Link another number' : 'Link your WhatsApp',
      children: [
        if (number == null) ...[
          Text('$what Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.', style: tkHint(t)),
          const SizedBox(height: 12),
        ],
        TkField(label: number != null ? 'Link another number instead' : 'Your WhatsApp number', controller: _number, keyboard: TextInputType.phone),
        Padding(
          padding: const EdgeInsets.only(top: 6, bottom: 12),
          child: Text('With the country code, e.g. +2348012345678', style: tkHint(t)),
        ),
        ProofField(auth: widget.auth, to: 'link a number', controller: _proof),
        Align(
          alignment: Alignment.centerLeft,
          child: _Btn(
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
              child: _Btn(
                label: 'Link',
                primary: true,
                onTap: busy || _code.text.length != 6
                    ? null
                    : () => run(() async {
                        await api.verifyWhatsAppBinding(body: WhatsAppVerifyRequest(code: _code.text));
                        _code.clear();
                        _number.clear();
                        _sent = false;
                        _another = false;
                        await _load();
                      }, done: 'WhatsApp linked'),
              ),
            ),
          ),
        ],
        if (error != null) TkErrorBox(error!),
      ],
      ),
      if (number != null && b.flows) _pinPart(t, b.pin),
    ]);
  }

  /// Account.tsx WhatsAppPin.
  Widget _pinPart(TkTokens t, WhatsAppPin pin) {
    final api = widget.auth.api;
    final look = TkLook.of(context);
    final f = factorsOf(widget.auth);
    final strong = f.passkey || f.totp;
    // As drawn: the PIN set, with Remove; Change the PIN (or Set a PIN)
    // opening its form; what the PIN is for under them.
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SettingsHeading('Approval PIN'),
        TkSection(
          inset: 56,
          children: [
            if (pin.set)
              TkListRow(
                title: 'PIN set${pin.setAt != null ? ' ${_fmt(pin.setAt!)}' : ''}',
                sub: pin.lockedUntil != null ? 'Locked until ${_fmt(pin.lockedUntil!)}' : 'Stands in for your passkey in WhatsApp',
                icon: 'b-shield',
                square: look.violet,
                trailing: TkSmallButton(
                  label: 'Remove',
                  semantics: 'Remove the PIN',
                  onPressed: busy
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
            TkListRow(
              title: pin.set ? 'Change the PIN' : 'Set a PIN',
              sub: strong ? '6 digits, not one repeated, nor a run like 123456' : 'Add a passkey or an authenticator first',
              icon: 'b-key',
              square: look.teal,
              onTap: strong ? () => setState(() => _pinOpen = !_pinOpen) : null,
            ),
          ],
        ),
        if (_pinOpen && strong) ...[
          const SizedBox(height: 14),
          Container(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
            decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkField(label: pin.set ? 'New PIN' : 'PIN', controller: _pin, digits: 6, obscure: true, keyboard: TextInputType.number),
                const SizedBox(height: 12),
                ProofField(auth: widget.auth, to: 'set the PIN', controller: _pinProof),
                ListenableBuilder(
                  listenable: _pin,
                  builder: (context, _) => TkSmallButton(
                    label: pin.set ? 'Change PIN' : 'Set PIN',
                    primary: true,
                    onPressed: busy || _pin.text.length != 6 || proofAsked(widget.auth) == null
                        ? null
                        : () => run(() async {
                            final p = proofOf(widget.auth, _pinProof.text);
                            await api.setWhatsAppPin(body: WhatsAppPinRequest(pin: _pin.text, totp: p.totp, password: p.password));
                            _pin.clear();
                            _pinProof.clear();
                            _pinOpen = false;
                            await _load();
                          }, done: 'PIN saved'),
                  ),
                ),
              ],
            ),
          ),
        ],
        if (error != null) ...[const SizedBox(height: 10), TkErrorBox(error!)],
        const SizedBox(height: 10),
        const SettingsNote(
          'Six digits you enter in WhatsApp to confirm approvals whose policy allows a WhatsApp PIN. Policies asking for a passkey or authenticator code still send you here. Five wrong PINs lock it for 15 minutes.',
        ),
      ],
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
            child: _Btn(
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
          child: _Btn(
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
              _Btn(label: 'Open Telegram', primary: true, onTap: () => openOutside(link)),
              _Btn(
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
            child: _Btn(
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
            child: _Btn(
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
