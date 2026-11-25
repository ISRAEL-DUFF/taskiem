// Left without ordering (docs/spec/forms-that-sell.md §6.6): an order
// form's follow-ups and who left it this week, each contact masked as the
// server gives it ("… 4567"), never more. It is where the chat's alert
// "3 people left your order form today" opens, as the web's
// /storefront/left/<id> (StorefrontLeft.tsx, lib/left.ts).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show FormLeft, FormLeftLeftItem, PatchStorefrontActionRequest;
import '../app/auth.dart';
import '../kit/forms.dart' show TkSwitch, errorWords;
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkListRow, TkRoute, TkSection;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart' show TkPageScreen;
import '../theme/look.dart';
import 'groups.dart' show pickOneOf;
import 'words.dart' show CardTone, whenWords;

/// Opens an order form's Left without ordering.
Future<void> openLeft(BuildContext context, Auth auth, String action, {String backLabel = 'Back'}) =>
    Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => LeftPage(auth: auth, action: action, backLabel: backLabel)));

/// left.ts WAITS: the waits the owner chooses from, in minutes.
const leftWaits = [10, 15, 30, 60, 120, 240, 720, 1440];

/// left.ts waitWords: "30 minutes", "1 hour", "24 hours".
String waitWords(int minutes) {
  if (minutes < 60) return '$minutes minutes';
  final h = minutes / 60;
  return h == 1 ? '1 hour' : '${h == h.roundToDouble() ? h.round() : h.toStringAsFixed(1)} hours';
}

/// left.ts choiceWords: "Smart Ear Cleaner, 2 items, to Lekki".
String choiceWords(FormLeftLeftItem r) => [
  r.product.isEmpty ? 'Your order form' : r.product,
  if (r.package != null) r.package == 1 ? '1 item' : '${r.package} items',
  if (r.zone != null && r.zone!.isNotEmpty) 'to ${r.zone}',
].join(', ');

/// A row's title, short enough beside its pill: "2 items to Lekki", or
/// the product when no package was chosen.
String rowTitle(FormLeftLeftItem r) {
  final pkg = r.package == null ? '' : (r.package == 1 ? '1 item' : '${r.package} items');
  final zone = r.zone == null || r.zone!.isEmpty ? '' : 'to ${r.zone}';
  final t = [pkg, zone].where((s) => s.isNotEmpty).join(' ');
  return t.isEmpty ? (r.product.isEmpty ? 'Your order form' : r.product) : t;
}

/// left.ts rowStatus: where a row is, in a pill.
(String, CardTone) leftStatus(FormLeftLeftItem r, bool held) {
  if (r.cameBackAt != null) return ('Came back', CardTone.lime);
  if (r.followedUpAt != null) return (r.channel == 'email' ? 'Emailed' : 'Texted', CardTone.plain);
  if (r.outcome != null) return (r.outcome == 'stopped' ? 'Stopped' : 'Not sent', CardTone.plain);
  return (held ? 'Held' : 'Waiting', held ? CardTone.warn : CardTone.plain);
}

const _outcomeWords = {
  'capped': 'Messaged enough today',
  'stopped': 'They stopped your messages',
  'limit': 'Plan’s month used',
  'off': 'Follow-ups were off',
  'stale': 'Too long ago',
  'failed': 'Could not be sent',
};

class LeftPage extends StatefulWidget {
  const LeftPage({super.key, required this.auth, required this.action, this.backLabel = 'Back'});
  final Auth auth;
  final String action;
  final String backLabel;

  @override
  State<LeftPage> createState() => _LeftPageState();
}

class _LeftPageState extends State<LeftPage> {
  FormLeft? _left;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getStorefrontActionLeft(id: widget.action);
      if (mounted) setState(() => (_left = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _set(PatchStorefrontActionRequest body, String done) async {
    setState(() => _busy = true);
    try {
      await widget.auth.api.patchStorefrontAction(id: widget.action, body: body);
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _pickWait(int now) async {
    final m = await pickOneOf(
      context,
      title: 'Wait before the message',
      choices: [for (final w in leftWaits) ('$w', waitWords(w), w == 30 ? 'As most sellers do' : '')],
      value: '$now',
    );
    if (m == null || !mounted) return;
    await _set(PatchStorefrontActionRequest(followupWait: int.parse(m)), 'They are followed up ${waitWords(int.parse(m))} after they leave');
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final d = _left;
    final now = DateTime.now();
    final held = d?.followups.heldAt != null;
    return TkPageScreen(
      title: 'Left without ordering',
      titleSize: 30,
      backLabel: widget.backLabel,
      description:
          'People who typed their phone or email into your order form and left without sending it. Taskiem keeps only how to reach them and what they chose, for 7 days, and sends them one message with a link to finish.',
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (d == null && _error == null) const TkLoading(rows: 3, label: 'Loading who left…'),
        if (d != null) ...[
          if (held)
            TkCallout(
              leading: TkSquare('b-warn', color: look.orange),
              title: 'Follow-ups held',
              sub: 'Many left at once. No messages go out until you release them.',
              color: look.orange,
              subLines: 4,
              button: 'Release follow-ups',
              onPressed: _busy ? null : () => _set(const PatchStorefrontActionRequest(releaseHold: true), 'Follow-ups released'),
            ),
          TkSection(
            title: d.label,
            inset: 56,
            children: [
              TkListRow(
                title: 'Follow up people who leave',
                sub: d.followups.on ? 'One text, or an email when they gave only that' : 'Off: your form keeps nothing of them',
                icon: 'b-send',
                square: look.teal,
                chevron: false,
                titleLines: 2,
                trailing: TkSwitch(
                  label: 'Follow up people who leave',
                  on: d.followups.on,
                  onChanged: _busy
                      ? null
                      : (on) => _set(PatchStorefrontActionRequest(followupOn: on), on ? 'Follow-ups are on' : 'Follow-ups are off'),
                ),
              ),
              if (d.followups.on)
                TkListRow(
                  title: 'Wait before the message',
                  sub: waitWords(d.followups.waitMinutes),
                  icon: 'b-clock',
                  square: look.violet,
                  semanticsLabel: 'Wait before the message: ${waitWords(d.followups.waitMinutes)}',
                  onTap: _busy ? null : () => _pickWait(d.followups.waitMinutes),
                ),
            ],
          ),
          TkSection(
            title: 'This week',
            inset: 56,
            children: [
              TkListRow(title: 'Left without ordering', icon: 'b-out', square: look.orange, amount: '${d.week.left}', chevron: false),
              TkListRow(title: 'Followed up', icon: 'b-send', square: look.teal, amount: '${d.week.followed}', chevron: false),
              TkListRow(title: 'Came back', icon: 'b-check', square: look.lime, amount: '${d.week.cameBack}', chevron: false),
            ],
          ),
          TkSection(
            title: 'People who left',
            count: d.left.isEmpty ? null : d.left.length,
            empty: 'Nobody has left this form without ordering in the last 7 days.',
            children: [
              for (final r in d.left)
                TkListRow(
                  title: rowTitle(r),
                  subLines: 3,
                  semanticsLabel: '${choiceWords(r)}, ${leftStatus(r, held).$1}',
                  sub: [
                    r.contact,
                    whenWords(r.at.toLocal(), now),
                    if (r.outcome != null && r.followedUpAt == null) _outcomeWords[r.outcome] ?? '',
                  ].join(' · '),
                  icon: r.contactKind == 'email' ? 'b-mail' : 'b-phone',
                  status: leftStatus(r, held).$1,
                  statusTone: leftStatus(r, held).$2,
                  chevron: false,
                ),
            ],
          ),
        ],
      ],
    );
  }
}
