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
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardButton;
import 'my_chat.dart' show sayFailure;
import 'words.dart' show dayWords, clock;

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
    return TkScreen(
      title: 'Your standing',
      onRefresh: _load,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('What Taskiem’s moderators decided about how you message people, and how to appeal.', style: tkHint(t)),
        ),
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(label: 'Loading your standing…'),
        if (s != null)
          TkPanel(
            padding: const EdgeInsets.all(16),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Semantics(header: true, child: Text(standingTitle(s.state), style: tkDisplay(size: 19, color: s.state == 'ok' ? t.text : look.redInk))),
                  const SizedBox(height: 6),
                  Text(s.words, style: tkText(size: 15, lineHeight: 1.45, color: t.text)),
                ],
              ),
            ],
          ),
        if (s != null && s.actions.isNotEmpty)
          TkSection(
            title: 'Decisions',
            children: [
              Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 8), child: Text(s.appealWords, style: tkHint(t))),
              for (final a in s.actions)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Semantics(
                    container: true,
                    label: '${actionName(a.action)}, ${_when(a.at, now)}',
                    child: TkPanel(
                      key: ValueKey('standing ${a.id}'),
                      padding: const EdgeInsets.all(16),
                      children: [
                        Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            Text(actionName(a.action), style: tkText(size: 16, weight: 700, color: t.text)),
                            Text(_when(a.at, now), style: tkHint(t)),
                            const SizedBox(height: 6),
                            Text('Why: ${a.reason}', style: tkText(size: 14.5, lineHeight: 1.4, color: t.text)),
                            const SizedBox(height: 4),
                            Text(a.inForce ? a.words : (a.liftedAt != null ? 'Lifted ${_when(a.liftedAt, now)}.' : 'No longer in force.'), style: tkText(size: 14, lineHeight: 1.4, color: t.muted)),
                            if (a.appeal is Map)
                              Padding(
                                padding: const EdgeInsets.only(top: 6),
                                child: Text(appealWords(StandingAppeal.fromJson((a.appeal as Map).cast<String, dynamic>())), style: tkText(size: 14, lineHeight: 1.4, color: t.text)),
                              ),
                            if (a.canAppeal) ...[
                              const SizedBox(height: 10),
                              CardButton(label: 'Appeal', semantics: 'Appeal the ${actionName(a.action).toLowerCase()}', onTap: () => _appeal(a)),
                            ],
                          ],
                        ),
                      ],
                    ),
                  ),
                ),
            ],
          ),
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
      ],
    );
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

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkSheet(
      title: 'Appeal the ${actionName(widget.action.action).toLowerCase()}',
      footer: TkCta(label: _busy ? 'Sending…' : 'Send the appeal', onTap: _busy ? null : _send),
      body: TkFormBody(
        children: [
          Text('A moderator other than the one who decided reads it. You can appeal each decision once.', style: tkHint(t)),
          TkKitField(label: 'Why you think this decision is wrong', child: TkTextBox(key: const ValueKey('appeal-text'), controller: _text, lines: 5, max: 2000)),
          if (_error != null) Semantics(liveRegion: true, child: TkErrorBox(_error!)),
        ],
      ),
    );
  }
}

/// The notice on You while moderators decided something the person has not
/// seen; read quietly (nothing shown when it cannot be read).
class StandingNotice extends StatelessWidget {
  const StandingNotice({super.key, required this.standing, required this.onOpen});
  final PersonStanding standing;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final s = standing;
    final title = s.state == 'ok' ? 'Taskiem’s moderators changed a decision about your account' : standingTitle(s.state);
    return Semantics(
      liveRegion: true,
      button: true,
      label: '$title. Read what Taskiem’s moderators decided',
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onOpen,
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(color: t.tkDangerBg, borderRadius: BorderRadius.circular(16)),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(title, style: tkText(size: 15, weight: 700, color: t.tkDangerInk)),
              const SizedBox(height: 2),
              Text('Read what Taskiem’s moderators decided', style: tkText(size: 14, weight: 600, color: look.on)),
            ],
          ),
        ),
      ),
    );
  }
}
