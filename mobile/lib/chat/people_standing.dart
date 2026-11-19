// Your standing with Taskiem's moderators (docs/spec/people-chatting.md §8.3;
// the server's As built, "stage 5a gaps, safety and moderation": `GET
// /v1/me/standing`, `POST /v1/me/standing/seen`, `POST
// /v1/me/standing/appeals`). What they decided about how the person messages
// people, in plain words, each decision with its reason, and an appeal of
// each (once); the notice on You while a decision is unseen; and where
// Taskiem publishes how many requests from the authorities it received
// (`/transparency` on the web). The same words as the web's YouStanding.tsx.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/config.dart' show taskiemUrl;
import '../app/links.dart' show openInBrowser;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/mobile.dart';
import '../kit/look.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;
import 'words.dart' show CardTone, dayWords, clock;

/// The address of the published count of lawful requests.
final transparencyUrl = Uri.parse('$taskiemUrl/transparency');

/// Opens the published count in the browser tab.
Future<void> openTransparency(BuildContext context) => openOrSay(context, () => openInBrowser(transparencyUrl), 'The page could not open. It is at $transparencyUrl');

/// The standing's title, as You shows it.
String standingTitle(String state) => switch (state) {
  'warned' => 'You have a warning',
  'limited' => 'Your message requests are limited',
  'suspended' => 'Your account can’t message people for now',
  'closing' => 'Taskiem is closing your account',
  _ => 'Your account is in good standing',
};

/// The name of a decision in the person's list.
String actionName(String action) => switch (action) {
  'warn' => 'Warning',
  'limit' => 'Limit on message requests',
  'suspend' => 'Suspension',
  'close' => 'Closure',
  _ => 'Decision',
};

/// What became of an appeal, in plain words.
String appealWords(StandingAppeal a) {
  final note = a.note is String && (a.note as String).isNotEmpty ? ' They said: ${a.note}' : '';
  return switch (a.state) {
    'upheld' => 'A moderator looked at your appeal and kept the decision.$note',
    'overturned' => 'A moderator agreed with your appeal and lifted the decision.$note',
    _ => 'You appealed. A moderator will read it and we will tell you what they decide.',
  };
}

/// The words for an appeal that did not go.
String appealFailure(Object e) {
  if (e is ApiError) {
    switch (e.status) {
      case 400:
        return 'Write why you think this decision is wrong (up to 2,000 characters).';
      case 404:
        return 'That decision isn’t on your account any more. Pull down to read your standing again.';
      case 409:
        return 'This decision was already appealed, or is no longer in force, so it can’t be appealed now.';
      case 429:
        return 'You have sent many appeals in the last hour. Try again later.';
    }
  }
  return sayFailure(e);
}

/// A send refused because the person's own account is suspended (403):
/// the server's words point to Your standing.
bool pointsToStanding(Object? e) => e is ApiError && e.status == 403 && e.message.contains('Your standing');

/// A moment, as the list says it.
String _when(Object? at, DateTime now) {
  final d = at is DateTime ? at : (at is String ? DateTime.tryParse(at) : null);
  return d == null ? '' : '${dayWords(d, now)}, ${clock(d)}';
}

/// You > Settings > Your standing (`/you/settings/standing`). Opening it
/// marks the decisions seen.
class YourStandingScreen extends StatefulWidget {
  const YourStandingScreen({super.key, required this.auth, this.now = DateTime.now});
  final Auth auth;
  final DateTime Function() now;

  @override
  State<YourStandingScreen> createState() => _YourStandingScreenState();
}

/// Told each time Your standing marked its decisions seen: the notice on
/// You's Chats reads the standing again.
final standingSeen = ValueNotifier<int>(0);

class _YourStandingScreenState extends State<YourStandingScreen> {
  PersonStanding? _s;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final s = await widget.auth.api.getMyStanding();
      if (!mounted) return;
      setState(() => (_s = s, _error = null));
      // Marked seen, You's notice reads again (however this was opened: its
      // notice, a push, Settings).
      if (s.unseen > 0) unawaited(widget.auth.api.seenMyStanding().catchError((Object _) {}).whenComplete(() => standingSeen.value++));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _appeal(PersonStandingActionsItem a) async {
    final said = await showTkSheet<String>(context, (_) => _AppealSheet(auth: widget.auth, action: a));
    if (said == null || !mounted) return;
    showTkToast(context, said);
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final s = _s;
    final now = widget.now();
    // A large-title page: where you stand on a top card, each decision a
    // row with its state (its whole story a tap away), the authorities'
    // requests a row.
    return TkPageScreen(
      title: 'Your standing',
      titleSize: 30,
      backLabel: 'Back to Settings',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(label: 'Loading your standing…'),
        if (s != null)
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('How you message people', color: s.state == 'ok' ? look.limeInk : look.redInk),
                const SizedBox(height: 6),
                Semantics(header: true, child: Text(standingTitle(s.state), style: tkDisplay(size: 22, letterSpacing: -0.5, color: s.state == 'ok' ? t.text : look.redInk))),
                const SizedBox(height: 6),
                Text(s.words, style: tkText(size: 14, lineHeight: 1.45, color: t.text)),
              ],
            ),
          ),
        if (s != null && s.actions.isNotEmpty) ...[
          TkSection(
            title: 'Decisions',
            inset: 56,
            children: [
              for (final a in s.actions)
                TkListRow(
                  key: ValueKey('standing ${a.id}'),
                  title: actionName(a.action),
                  sub: '${_when(a.at, now)} · ${a.reason}',
                  icon: 'b-shield',
                  square: a.inForce ? look.red : look.teal,
                  status: a.inForce ? 'In force' : 'Lifted',
                  statusTone: a.inForce ? CardTone.danger : CardTone.plain,
                  semanticsLabel: '${actionName(a.action)}, ${_when(a.at, now)}, ${a.inForce ? 'in force' : 'lifted'}${a.canAppeal ? '. Open it to appeal' : ''}',
                  onTap: () => _decision(a),
                ),
            ],
          ),
          Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text(s.appealWords, style: tkText(size: 13, lineHeight: 1.4, color: t.muted))),
        ],
        if (s != null)
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Requests from the authorities',
                sub: 'How many Taskiem received, published every half-year',
                icon: 'info',
                square: look.blue,
                onTap: () => openTransparency(context),
              ),
            ],
          ),
        // What the page is, quiet under it.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('What Taskiem’s moderators decided about how you message people, and how to appeal.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        ),
      ],
    );
  }

  /// One decision in full: when, why, what it means now, the appeal; and
  /// Appeal while it can be.
  Future<void> _decision(PersonStandingActionsItem a) async {
    final now = widget.now();
    final appeal = await showTkSheet<bool>(
      context,
      (c) => TkAppLook(
        child: TkSheet(
          title: actionName(a.action),
          crumb: _when(a.at, now),
          bareFooter: a.canAppeal,
          footer: a.canAppeal ? TkCta(label: 'Appeal', onTap: () => Navigator.of(c).pop(true)) : null,
          body: TkFormBody(
            children: [
              TkFormGroup(
                children: [
                  TkLines([
                    ('Why', a.reason),
                    ('Now', a.inForce ? 'In force' : (a.liftedAt != null ? 'Lifted ${_when(a.liftedAt, now)}' : 'No longer in force')),
                  ]),
                ],
              ),
              if (a.inForce) Text(a.words, style: tkText(size: 14, lineHeight: 1.45, color: TkTheme.tokens(c).text)),
              if (a.appeal is Map) Text(appealWords(StandingAppeal.fromJson((a.appeal as Map).cast<String, dynamic>())), style: tkText(size: 14, lineHeight: 1.45, color: TkTheme.tokens(c).muted)),
            ],
          ),
        ),
      ),
    );
    if (appeal == true && mounted) await _appeal(a);
  }
}

/// Appeal one decision: why the person thinks it is wrong.
class _AppealSheet extends StatefulWidget {
  const _AppealSheet({required this.auth, required this.action});
  final Auth auth;
  final PersonStandingActionsItem action;

  @override
  State<_AppealSheet> createState() => _AppealSheetState();
}

class _AppealSheetState extends State<_AppealSheet> {
  final _text = TextEditingController();
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final text = _text.text.trim();
    if (text.isEmpty) return setState(() => _error = 'Write why you think this decision is wrong.');
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.api.appealMyStanding(body: AppealMyStandingRequest(actionId: widget.action.id, text: text));
      if (mounted) Navigator.of(context).pop(r.said.isEmpty ? 'Appeal sent' : r.said);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = appealFailure(e), _busy = false));
    }
  }

  // The app's form sheet, as a closed group's appeal.
  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Appeal the ${actionName(widget.action.action).toLowerCase()}',
    crumb: 'A moderator who didn’t decide reads it',
    action: _busy ? 'Sending…' : 'Send the appeal',
    onAction: _busy ? null : _send,
    children: [
      TkFormGroup(
        children: [
          TkKitField(
            label: 'Why you think this decision is wrong',
            hint: 'You can appeal each decision once.',
            child: TkTextBox(key: const ValueKey('appeal-text'), controller: _text, lines: 5, max: 2000),
          ),
        ],
      ),
      if (_error != null) Semantics(liveRegion: true, child: TkErrorBox(_error!)),
    ],
  );
}

/// The notice on You while moderators decided something the person has not
/// seen; read quietly (nothing shown when it cannot be read).
class StandingNotice extends StatelessWidget {
  const StandingNotice({super.key, required this.standing, required this.onOpen});
  final PersonStanding standing;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final s = standing;
    final title = s.state == 'ok' ? 'Taskiem’s moderators changed a decision about your account' : standingTitle(s.state);
    // The app's callout, red, with Read as its button.
    return Semantics(
      liveRegion: true,
      child: TkCallout(
        leading: TkSquare('b-shield', color: look.red),
        title: title,
        sub: 'Read what Taskiem’s moderators decided',
        color: look.red,
        button: 'Read',
        semantics: 'Read what Taskiem’s moderators decided',
        onPressed: onOpen,
      ),
    );
  }
}
