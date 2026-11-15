// The team (web Staff.tsx, StaffPerson.tsx, Tasks.tsx; lib/team.ts,
// lib/tasks.ts): the people of the business, added by phone (they join from
// a WhatsApp message), who is in today and who is late, the invitations
// waiting, a person's job, hours, pay for the record and how they are
// doing; and tasks: the person's own (I'll do it, Done, with a photo when
// asked, Can't), and for those who give them, everyone's (Remind, Give to
// someone else, Call off) and those that repeat.

import 'package:flutter/widgets.dart';
import 'package:image_picker/image_picker.dart' show ImagePicker, ImageSource;

import '../app/lock.dart' show stepOut;
import '../api/api.g.dart' show ListStaffTasksResponsePeopleItem;
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'give_task.dart' show GiveTask, roleWords, weekdays, whoChoices;
import 'money_sheets.dart' show dateLabel;
import 'words.dart' show CardTone, naira;
import '../kit/mobile.dart' show TkRoute;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';
List<String> _strs(Object? v) => (v as List? ?? const []).whereType<String>().toList();

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Staff.
Future<void> openStaff(BuildContext context, Auth auth) => _push(context, StaffPage(auth: auth));

/// Opens Tasks.
Future<void> openTasks(BuildContext context, Auth auth) => _push(context, TasksPage(auth: auth));

DateTime _lagos(DateTime t) => t.toUtc().add(const Duration(hours: 1));
String _hm(String iso) {
  final d = DateTime.tryParse(iso);
  if (d == null) return '';
  final l = _lagos(d);
  return '${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
}

String _day(DateTime t) {
  final l = _lagos(t);
  return '${l.year.toString().padLeft(4, '0')}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
}

// --- team.ts ---

/// team.ts STAFF_ROLES.
const staffRoles = [
  ('cashier', 'Cashier', 'Sells, keeps the stock and works the orders.'),
  ('shift_manager', 'Shift manager', 'A cashier’s work, plus payments and handing the shift over.'),
  ('admin', 'Admin', 'Almost everything, but not billing or owners.'),
];

/// team.ts staffProblem.
String? staffProblem(String name, String phone, String role) {
  if (name.trim().isEmpty) return 'Give their name.';
  if (name.trim().length > 80) return 'A name is at most 80 letters.';
  final digits = phone.replaceAll(RegExp(r'\D'), '');
  if (digits.length < 7 || digits.length > 15) return 'Give their mobile number, like 0803 123 4567.';
  if (role.isEmpty) return 'Choose what they do.';
  return null;
}

/// team.ts personSub.
String personSub(Map<String, dynamic> p) =>
    [_strs(p['roles']).map(roleWords).join(', '), _str(p['phone']).isNotEmpty ? _str(p['phone']) : _str(p['email'])].where((x) => x.isNotEmpty).join(' · ');

/// team.ts inviteSub.
String inviteSub(Map<String, dynamic> x) => [
  _strs(x['roles']).map(roleWords).join(', '),
  _str(x['phone']),
  _str(x['sent_at']).isNotEmpty ? 'sent ${dateLabel(_str(x['sent_at']))}' : 'not sent yet',
].join(' · ');

/// team.ts lengthWords.
String lengthWords(Duration d) {
  final mins = (d.inSeconds / 60).round();
  final h = mins ~/ 60, m = mins % 60;
  String part(int n, String one) => '$n $one${n == 1 ? '' : 's'}';
  if (h == 0) return part(m, 'minute');
  if (m == 0) return part(h, 'hour');
  return '${part(h, 'hour')} ${part(m, 'minute')}';
}

/// team.ts shiftWords.
String shiftWords(Map<String, dynamic> s) {
  if (_str(s['out_at']).isEmpty) return 'Since ${_hm(_str(s['in_at']))}';
  final how = s['out_how'] == 'auto' ? ' · closed by Taskiem' : (s['out_how'] == 'handover' ? ' · handed over' : '');
  final len = DateTime.parse(_str(s['out_at'])).difference(DateTime.parse(_str(s['in_at'])));
  return '${_hm(_str(s['in_at']))} to ${_hm(_str(s['out_at']))} · ${lengthWords(len)}$how';
}

const _short = {'mon': 'Mon', 'tue': 'Tue', 'wed': 'Wed', 'thu': 'Thu', 'fri': 'Fri', 'sat': 'Sat', 'sun': 'Sun'};

/// team.ts rotaWords: days in a row with the same hours said once.
String rotaWords(List<Map<String, dynamic>> rota) {
  if (rota.isEmpty) return 'No usual hours set';
  final order = [for (final d in weekdays) d.$1];
  final sorted = [...rota]..sort((a, b) => order.indexOf(_str(a['day'])) - order.indexOf(_str(b['day'])));
  final runs = <({String from, String to, String start, String end})>[];
  for (final r in sorted) {
    final last = runs.isEmpty ? null : runs.last;
    if (last != null && last.start == r['start'] && last.end == r['end'] && order.indexOf(_str(r['day'])) == order.indexOf(last.to) + 1) {
      runs[runs.length - 1] = (from: last.from, to: _str(r['day']), start: last.start, end: last.end);
    } else {
      runs.add((from: _str(r['day']), to: _str(r['day']), start: _str(r['start']), end: _str(r['end'])));
    }
  }
  return runs.map((r) => '${r.from == r.to ? _short[r.from] : '${_short[r.from]} to ${_short[r.to]}'}, ${r.start} to ${r.end}').join('; ');
}

const _per = {'hour': 'an hour', 'day': 'a day', 'week': 'a week', 'month': 'a month'};

/// team.ts payWords.
String payWords(Map<String, dynamic> p) => p['pay_kobo'] is num && _str(p['pay_per']).isNotEmpty ? '${naira(_int(p['pay_kobo']))} ${_per[p['pay_per']]}' : '';

/// team.ts figureRows: a person's figures as label and words.
List<(String, String)> figureRows(Map<String, dynamic> f) {
  String count(int n, String one) => '$n $one${n == 1 ? '' : 's'}';
  final rows = <(String, String)>[];
  final shifts = _int(f['shifts']);
  rows.add(('Hours', shifts == 0 ? 'None clocked' : '${lengthWords(Duration(minutes: _int(f['minutes'])))} over ${count(shifts, 'shift')}'));
  final lateDays = _strs(f['late_days']).length, missed = _strs(f['missed_days']).length;
  final late = [if (lateDays > 0) 'late on ${count(lateDays, 'day')}', if (missed > 0) 'did not come on ${count(missed, 'day')} they were due'].join(', ');
  if (late.isNotEmpty) rows.add(('Time keeping', late[0].toUpperCase() + late.substring(1)));
  final sales = _map(f['sales']);
  if (_int(sales['count']) > 0) {
    final by = _map(sales['by_how']);
    const words = {'cash': 'Cash', 'transfer': 'Transfer', 'pos': 'POS'};
    final how = [
      for (final k in const ['cash', 'transfer', 'pos'])
        if (by[k] != null) '${words[k]} ${naira(_int(by[k]))}',
    ];
    rows.add(('Sales', '${naira(_int(sales['total_kobo']))} in ${count(_int(sales['count']), 'sale')} (${how.join(', ')})'));
  } else {
    rows.add(('Sales', 'None recorded'));
  }
  final hand = _maps(f['handovers']);
  if (hand.isNotEmpty) {
    final known = hand.where((h) => h['diff_kobo'] != null).toList();
    final diff = known.fold(0, (n, h) => n + _int(h['diff_kobo']));
    final cash = known.isEmpty ? '' : (diff < 0 ? ', ${naira(-diff)} short' : (diff > 0 ? ', ${naira(diff)} over' : ', the cash right'));
    rows.add(('Handovers', '${hand.length}$cash'));
  }
  rows.add(('Orders worked', _int(f['orders']) == 0 ? 'None' : '${_int(f['orders'])}'));
  final t = _map(f['tasks']);
  final parts = [
    if (_int(t['done']) > 0) '${_int(t['done'])} done${_int(t['done_late']) > 0 ? ' (${_int(t['done_late'])} late)' : ''}',
    if (_int(t['open']) > 0) '${_int(t['open'])} open${_int(t['late']) > 0 ? ' (${_int(t['late'])} late)' : ''}',
    if (_int(t['cant']) > 0) '${_int(t['cant'])} sent back',
  ];
  rows.add(('Tasks', parts.isEmpty ? 'None' : parts.join(', ')));
  return rows;
}

/// team.ts profileProblem, for the form's boxes.
String? profileProblem({required String title, required String note, required String pay, required Map<String, ({bool on, String start, String end})> days}) {
  if (title.trim().length > 60) return 'A job title is at most 60 letters.';
  if (note.trim().length > 500) return 'A note is at most 500 letters.';
  final p = pay.replaceAll(RegExp(r'[,\s₦]'), '');
  if (p.isNotEmpty && !RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(p)) return 'The pay is a number of naira, like 80000.';
  for (final (d, label) in weekdays) {
    final x = days[d]!;
    if (!x.on) continue;
    if (!RegExp(r'^\d{2}:\d{2}$').hasMatch(x.start) || !RegExp(r'^\d{2}:\d{2}$').hasMatch(x.end)) return 'Give $label’s hours, like 08:00 and 16:00.';
    if (x.start == x.end) return '$label starts and ends at the same time.';
  }
  return null;
}

// --- tasks.ts ---

/// tasks.ts taskWho.
String taskWho(Map<String, dynamic> t) {
  if (_str(t['to_role']).isEmpty) return _str(t['person']).isNotEmpty ? _str(t['person']) : 'Someone';
  final r = 'any ${roleWords(_str(t['to_role'])).toLowerCase()}';
  return _str(t['taken_by']).isNotEmpty && _str(t['person']).isNotEmpty ? '${t['person']} ($r)' : r;
}

const _weekdaysShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// tasks.ts dueWords.
String dueWords(String dueAt, DateTime now) {
  final d = DateTime.tryParse(dueAt);
  if (d == null) return '';
  final k = _day(d);
  final hm = _hm(dueAt);
  if (k == _day(now)) return 'by $hm';
  if (k == _day(now.add(const Duration(days: 1)))) return 'by tomorrow $hm';
  if (k == _day(now.subtract(const Duration(days: 1)))) return 'by yesterday $hm';
  final l = _lagos(d);
  return 'by ${_weekdaysShort[l.weekday - 1]} ${l.day} ${_months[l.month - 1]}, $hm';
}

/// tasks.ts taskSub.
String taskSub(Map<String, dynamic> t, bool mine, DateTime now) {
  final s = _str(t['state']);
  return [
    if (!mine) taskWho(t) else if (_str(t['to_role']).isNotEmpty && _str(t['taken_by']).isEmpty) 'for ${taskWho(t)}',
    if (s == 'open' && _str(t['due_at']).isNotEmpty) dueWords(_str(t['due_at']), now),
    if (s == 'done') _str(t['done_at']).isNotEmpty ? 'done at ${_hm(_str(t['done_at']))}' : 'done',
    if (s == 'cant') _str(t['cant_why']).isNotEmpty ? 'can’t: “${t['cant_why']}”' : 'sent back',
    if (s == 'cancelled') 'called off',
    if (t['photo'] == true && s == 'open') 'photo when done',
    if (mine && _str(t['giver']).isNotEmpty) 'from ${t['giver']}',
  ].join(' · ');
}

/// tasks.ts taskBadge.
({String text, CardTone tone})? taskBadge(Map<String, dynamic> t) => switch (t['state']) {
  'done' => (text: 'Done', tone: CardTone.lime),
  'cant' => (text: 'Can’t', tone: CardTone.danger),
  'cancelled' => (text: 'Called off', tone: CardTone.plain),
  _ => t['late'] == true ? (text: 'Late', tone: CardTone.warn) : null,
};

/// tasks.ts repeatWords.
String repeatWords(Map<String, dynamic> r) {
  if (r['every'] == 'day') return 'Every day at ${r['at']}';
  return 'Every ${weekdays.where((w) => w.$1 == r['day']).map((w) => w.$2).firstOrNull ?? 'week'} at ${r['at']}';
}

Widget _notice(TkTokens t, String text) => Semantics(
  liveRegion: true,
  container: true,
  child: Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
    child: Text(text, style: tkText(size: 15, color: t.text)),
  ),
);

// --- Staff ---

/// The people of the business: added by phone, who is in today, the
/// invitations waiting, removing someone who has left.
class StaffPage extends StatefulWidget {
  const StaffPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<StaffPage> createState() => _StaffPageState();
}

class _StaffPageState extends State<StaffPage> with Polling {
  Map<String, dynamic>? _view;
  Map<String, dynamic>? _day;
  List<String> _own = const [];
  String? _error;
  String? _actError;
  bool _busy = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
    _auth.transport.send('GET', '/v1/roles').then((r) {
      final names = [for (final x in _maps((r as Map)['roles'])) _str(x['name'])];
      if (mounted) setState(() => _own = names.where((n) => n != 'owner' && !staffRoles.any((s) => s.$1 == n)).toList());
    }, onError: (Object _) {});
  }

  Future<void> _load() async {
    try {
      final v = (await _auth.transport.send('GET', '/v1/staff') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_view = v, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    try {
      final d = (await _auth.transport.send('GET', '/v1/staff/clock') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _day = d);
    } on Object {
      // No day shown.
    }
  }

  Future<void> _run(Future<String?> Function() f) async {
    setState(() => (_busy = true, _actError = null));
    try {
      final said = await f();
      if (mounted && said != null) showTkToast(context, said);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _add() async {
    final out = await showTkModal<({bool sent, String name})>(
      context,
      title: 'Add staff',
      builder: (_) => _AddStaff(auth: _auth, own: _own),
    );
    if (out == null || !mounted) return;
    showTkToast(
      context,
      out.sent ? 'Invitation sent to ${out.name}' : '${out.name} is added, but the WhatsApp message did not go. Try Send again in a little while.',
      error: !out.sent,
    );
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _view;
    final d = _day;
    final late = d == null ? const <Map<String, dynamic>>[] : _maps(d['late']);
    final shifts = d == null ? const <Map<String, dynamic>>[] : _maps(d['shifts']);
    final invites = v == null ? const <Map<String, dynamic>>[] : _maps(v['invites']);
    final people = v == null ? const <Map<String, dynamic>>[] : _maps(v['people']);
    return TkPageScreen(
      title: 'Staff',
      description:
          'The people who work with you. Add someone by their phone number: they get a WhatsApp message from Taskiem and join with one tap, no email needed.',
      onRefresh: _load,
      action: TkSmallButton(label: 'Add', semantics: 'Add staff', primary: true, onPressed: _add),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (_actError != null) TkErrorBox(_actError!),
        if (v == null && _error == null) const TkLoading(rows: 4, label: 'Loading your staff…'),
        if (late.isNotEmpty || shifts.isNotEmpty)
          TkPageCard(
            title: 'Today',
            children: [
              Text('Staff say in and out to Taskiem on WhatsApp when they start and finish.', style: tkHint(t)),
              for (final l in late)
                TkRow(
                  name: _str(l['name']),
                  letters: _str(l['name']),
                  sub: 'Due at ${l['due']}',
                  pill: const TkPill('Not in yet', tone: CardTone.warn),
                ),
              for (final x in shifts)
                TkRow(
                  name: _str(x['name']).isNotEmpty ? _str(x['name']) : 'Someone',
                  letters: _str(x['name']).isNotEmpty ? _str(x['name']) : '?',
                  sub: shiftWords(x),
                  pill: _str(x['out_at']).isEmpty ? const TkPill('In now', tone: CardTone.lime) : null,
                  // Where they clocked in, when they shared it: a map.
                  onTap: _str(x['in_where']).isEmpty
                      ? null
                      : () => openOrSay(context, () => openOutside(Uri.parse(_str(x['in_where']))), 'The map could not be opened.'),
                ),
            ],
          ),
        if (invites.isNotEmpty)
          TkPageCard(
            title: 'Waiting to join',
            children: [
              Text('They tap Join in the WhatsApp message Taskiem sent them. An invitation lasts seven days.', style: tkHint(t)),
              for (final x in invites)
                () {
                  final who = _str(x['name']).isNotEmpty ? _str(x['name']) : _str(x['phone']);
                  return Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        TkRow(name: who, letters: who, sub: inviteSub(x)),
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: [
                            TkSmallButton(
                              label: 'Send again',
                              semantics: 'Send again to $who',
                              onPressed: _busy
                                  ? null
                                  : () => _run(() async {
                                      final r =
                                          (await _auth.transport.send('POST', '/v1/staff/invites/${Uri.encodeComponent(_str(x['id']))}/send', body: const {})
                                              as Map?) ??
                                          const {};
                                      if (r['sent'] != true) throw StateError('The WhatsApp message did not go. Try again in a little while.');
                                      return 'Sent again to $who';
                                    }),
                            ),
                            TkSmallButton(
                              label: 'Cancel',
                              semantics: 'Cancel the invitation to $who',
                              danger: true,
                              onPressed: _busy
                                  ? null
                                  : () async {
                                      final yes = await showTkConfirm(
                                        context,
                                        title: 'Cancel the invitation to $who?',
                                        body: 'Join will no longer work.',
                                        ok: 'Cancel it',
                                        cancel: 'Keep it',
                                        danger: true,
                                      );
                                      if (!yes) return;
                                      await _run(() async {
                                        await _auth.transport.send('DELETE', '/v1/staff/invites/${Uri.encodeComponent(_str(x['id']))}');
                                        return 'Invitation cancelled';
                                      });
                                    },
                            ),
                          ],
                        ),
                      ],
                    ),
                  );
                }(),
            ],
          ),
        if (v != null)
          TkPageCard(
            title: 'Your staff',
            children: [
              if (people.isEmpty) Text('No one yet. Add the people who work with you.', style: tkHint(t)),
              for (final p in people)
                () {
                  final name = _str(p['name']).isNotEmpty ? _str(p['name']) : _str(p['email']);
                  return Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        TkRow(
                          name: '$name${p['you'] == true ? ' (you)' : ''}',
                          letters: name,
                          sub: personSub(p),
                          pill: TkPill(p['chat'] == true ? 'On chat' : 'Web only', tone: p['chat'] == true ? CardTone.lime : CardTone.plain),
                          onTap: () => _go(StaffPersonPage(auth: _auth, user: _str(p['user_id']))),
                        ),
                        if (p['you'] != true)
                          Align(
                            alignment: Alignment.centerLeft,
                            child: TkSmallButton(
                              label: 'Remove',
                              semantics: 'Remove $name',
                              danger: true,
                              onPressed: _busy
                                  ? null
                                  : () async {
                                      final yes = await showTkConfirm(
                                        context,
                                        title: 'Remove $name from the business?',
                                        body: 'They lose ${_strs(p['roles']).map(roleWords).join(', ')}; what they did stays.',
                                        ok: 'Remove',
                                        cancel: 'Keep them',
                                        danger: true,
                                      );
                                      if (!yes) return;
                                      await _run(() async {
                                        await _auth.transport.send('POST', '/v1/staff/${Uri.encodeComponent(_str(p['user_id']))}/remove', body: const {});
                                        return '$name removed';
                                      });
                                    },
                            ),
                          ),
                      ],
                    ),
                  );
                }(),
            ],
          ),
      ],
    );
  }

  Future<void> _go(Widget page) async {
    await _push(context, page);
    await _load();
  }
}

class _AddStaff extends StatefulWidget {
  const _AddStaff({required this.auth, required this.own});
  final Auth auth;
  final List<String> own;

  @override
  State<_AddStaff> createState() => _AddStaffState();
}

class _AddStaffState extends State<_AddStaff> {
  final _name = TextEditingController();
  final _phone = TextEditingController();
  String _role = 'cashier';
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
    _phone.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = staffProblem(_name.text, _phone.text, _role);
    final help = staffRoles.where((r) => r.$1 == _role).map((r) => r.$3).firstOrNull;
    final choices = [for (final r in staffRoles) (r.$1, r.$2), for (final r in widget.own) (r, roleWords(r))];
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkKitField(
          label: 'Their name',
          child: TkTextBox(controller: _name, max: 80, plain: true),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Their mobile number',
          hint: 'Taskiem sends them a WhatsApp message to join.',
          child: TkTextBox(controller: _phone, phone: true, hint: '0803 123 4567'),
        ),
        const SizedBox(height: 10),
        TkKitField(
          field: false,
          label: 'What they do',
          hint: help,
          child: Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: choices.where((c) => c.$1 == _role).map((c) => c.$2).firstOrNull ?? _role,
              semantics: 'What they do: ${roleWords(_role)}',
              onPressed: () async {
                final v = await showTkModalChoices(context, title: 'What they do', value: _role, choices: choices);
                if (v != null) setState(() => _role = v);
              },
            ),
          ),
        ),
        if (_tried && problem != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Send the invitation',
              primary: true,
              enabled: !_busy,
              onPressed: () async {
                setState(() => _tried = true);
                if (problem != null) return;
                setState(() => (_busy = true, _error = null));
                try {
                  final r =
                      (await widget.auth.transport.send(
                        'POST',
                        '/v1/staff/invites',
                        body: {
                          'name': _name.text.trim(),
                          'phone': _phone.text.trim(),
                          'roles': [_role],
                        },
                      ) as Map?)?.cast<String, dynamic>() ??
                      const {};
                  if (context.mounted) Navigator.of(context).pop((sent: r['sent'] == true, name: _name.text.trim()));
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}

// --- one person ---

/// One person of the business: their job, usual hours and pay for the
/// record, and how they are doing.
class StaffPersonPage extends StatefulWidget {
  const StaffPersonPage({super.key, required this.auth, required this.user});
  final Auth auth;
  final String user;

  @override
  State<StaffPersonPage> createState() => _StaffPersonPageState();
}

class _StaffPersonPageState extends State<StaffPersonPage> {
  Map<String, dynamic>? _p;
  String? _error;
  String _period = 'week';
  Map<String, dynamic>? _figures;
  String? _figuresError;

  String get _path => '/v1/staff/${Uri.encodeComponent(widget.user)}';

  @override
  void initState() {
    super.initState();
    _load();
    _loadFigures();
  }

  Future<void> _load() async {
    try {
      final p = (await widget.auth.transport.send('GET', _path) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_p = p, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _loadFigures() async {
    final asked = _period;
    setState(() => (_figures = null, _figuresError = null));
    try {
      final f = (await widget.auth.transport.send('GET', '$_path/figures', query: {'period': asked}) as Map).cast<String, dynamic>();
      if (mounted && asked == _period) setState(() => _figures = f);
    } on Object catch (e) {
      if (mounted && asked == _period) setState(() => _figuresError = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = _p;
    final name = p == null ? '' : (_str(p['name']).isNotEmpty ? _str(p['name']) : (_str(p['email']).isNotEmpty ? _str(p['email']) : 'Someone'));
    final pr = p == null ? const <String, dynamic>{} : _map(p['profile']);
    final f = _figures;
    Widget kv(String k, String v) => MergeSemantics(
      child: Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(k, style: tkText(size: 13, weight: 600, color: t.muted)),
            Text(v, style: tkText(size: 15, color: t.text)),
          ],
        ),
      ),
    );
    return TkPageScreen(
      title: p == null ? 'Staff' : name,
      description: 'Their job, their usual hours and their pay, for your records. Taskiem does not pay staff.',
      backLabel: 'Back to Staff',
      onRefresh: () async {
        await _load();
        await _loadFigures();
      },
      action: p == null
          ? null
          : TkSmallButton(
              label: 'Change',
              semantics: 'Change $name',
              primary: true,
              onPressed: () async {
                final saved = await showTkModal<bool>(
                  context,
                  title: 'Change $name',
                  builder: (_) => _EditProfile(auth: widget.auth, path: _path, profile: pr),
                );
                if (saved == true && mounted) {
                  showTkToast(this.context, 'Saved');
                  await _load();
                }
              },
            ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 4, label: 'Loading…'),
        if (p != null)
          TkPageCard(
            title: _str(pr['title']).isNotEmpty ? _str(pr['title']) : 'About',
            children: [
              kv('What they do', personSub(p)),
              kv('On chat', p['chat'] == true ? 'Yes, Taskiem reaches them on WhatsApp' : 'Not yet'),
              if (_str(pr['started_on']).isNotEmpty) kv('Started', dateLabel(_str(pr['started_on']))),
              kv('Usual hours', rotaWords(_maps(pr['rota']))),
              kv('Pay', payWords(pr).isNotEmpty ? payWords(pr) : 'Not set'),
              if (_str(pr['note']).isNotEmpty) kv('Note', _str(pr['note'])),
            ],
          ),
        if (p != null)
          TkPageCard(
            title: 'How ${name.split(' ').first} is doing',
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final (v, label) in const [('day', 'Today'), ('week', 'This week'), ('month', 'This month')])
                    Semantics(
                      selected: _period == v,
                      child: TkSmallButton(
                        label: label,
                        primary: _period == v,
                        onPressed: () {
                          if (_period == v) return;
                          setState(() => _period = v);
                          _loadFigures();
                        },
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 10),
              if (_figuresError != null) TkErrorBox(_figuresError!),
              if (f == null && _figuresError == null) const TkLoading(rows: 3, label: 'Loading their figures…'),
              if (f != null)
                for (final (k, v) in figureRows(f)) kv(k, v),
            ],
          ),
      ],
    );
  }
}

class _EditProfile extends StatefulWidget {
  const _EditProfile({required this.auth, required this.path, required this.profile});
  final Auth auth;
  final String path;
  final Map<String, dynamic> profile;

  @override
  State<_EditProfile> createState() => _EditProfileState();
}

class _EditProfileState extends State<_EditProfile> {
  late final _title = TextEditingController(text: _str(widget.profile['title']));
  late final _started = TextEditingController(text: _str(widget.profile['started_on']));
  late final _note = TextEditingController(text: _str(widget.profile['note']));
  late final _pay = TextEditingController(
    text: widget.profile['pay_kobo'] is num
        ? (_int(widget.profile['pay_kobo']) / 100).toStringAsFixed(_int(widget.profile['pay_kobo']) % 100 == 0 ? 0 : 2)
        : '',
  );
  late String _per = _str(widget.profile['pay_per']).isNotEmpty ? _str(widget.profile['pay_per']) : 'month';
  late final Map<String, ({bool on, TextEditingController start, TextEditingController end})> _days = {
    for (final (d, _) in weekdays)
      d: () {
        final r = _maps(widget.profile['rota']).where((x) => x['day'] == d).firstOrNull;
        return (
          on: r != null,
          start: TextEditingController(text: r == null ? '08:00' : _str(r['start'])),
          end: TextEditingController(text: r == null ? '16:00' : _str(r['end'])),
        );
      }(),
  };
  bool _tried = false;
  bool _busy = false;
  String? _error;

  List<TextEditingController> get _boxes => [
    _title,
    _started,
    _note,
    _pay,
    for (final d in _days.values) ...[d.start, d.end],
  ];

  @override
  void initState() {
    super.initState();
    for (final c in _boxes) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in _boxes) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final days = {for (final e in _days.entries) e.key: (on: e.value.on, start: e.value.start.text.trim(), end: e.value.end.text.trim())};
    var problem = profileProblem(title: _title.text, note: _note.text, pay: _pay.text, days: days);
    if (problem == null && _started.text.trim().isNotEmpty && !RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(_started.text.trim())) {
      problem = 'The day they started does not look right.';
    }
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkKitField(
          label: 'Job title',
          hint: 'Like Head chef or Cashier, up to 60 letters.',
          child: TkTextBox(controller: _title, max: 60),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'The day they started',
          hint: 'As 2026-11-12.',
          child: TkTextBox(controller: _started, hint: '2026-11-12'),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Pay, for your records (₦)',
          hint: 'Leave it empty for none. Only people who manage staff see it.',
          child: TkTextBox(controller: _pay, money: true, hint: '80000'),
        ),
        const SizedBox(height: 6),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final (v, w) in const [('hour', 'an hour'), ('day', 'a day'), ('week', 'a week'), ('month', 'a month')])
              Semantics(
                selected: _per == v,
                child: TkSmallButton(label: w, semantics: 'Per: $w', primary: _per == v, onPressed: () => setState(() => _per = v)),
              ),
          ],
        ),
        const SizedBox(height: 12),
        Text('Usual hours', style: tkText(size: 13, weight: 600, color: t.muted)),
        Text('Clocking in is measured against these. An end before the start runs past midnight.', style: tkHint(t)),
        for (final (d, label) in weekdays) ...[
          TkChoiceRow(
            control: TkCheckbox(checked: _days[d]!.on),
            label: Text(label, style: tkText(size: 15, color: t.text)),
            onTap: () => setState(() => _days[d] = (on: !_days[d]!.on, start: _days[d]!.start, end: _days[d]!.end)),
          ),
          if (_days[d]!.on)
            Padding(
              padding: const EdgeInsets.only(left: 36, bottom: 6),
              child: Wrap(
                spacing: 10,
                runSpacing: 6,
                children: [
                  SizedBox(
                    width: 120,
                    child: TkKitField(
                      label: '$label from',
                      child: TkTextBox(controller: _days[d]!.start, hint: '08:00'),
                    ),
                  ),
                  SizedBox(
                    width: 120,
                    child: TkKitField(
                      label: '$label to',
                      child: TkTextBox(controller: _days[d]!.end, hint: '16:00'),
                    ),
                  ),
                ],
              ),
            ),
        ],
        const SizedBox(height: 10),
        TkKitField(
          label: 'Note',
          child: TkTextBox(controller: _note, lines: 3, max: 500),
        ),
        if (_tried && problem != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Save',
              primary: true,
              enabled: !_busy,
              onPressed: () async {
                setState(() => _tried = true);
                if (problem != null) return;
                final pay = _pay.text.replaceAll(RegExp(r'[,\s₦]'), '');
                final body = {
                  'title': _title.text.trim(),
                  if (_started.text.trim().isNotEmpty) 'started_on': _started.text.trim(),
                  'note': _note.text.trim(),
                  if (pay.isNotEmpty) ...{'pay_kobo': nairaKobo(pay), 'pay_per': _per},
                  'rota': [
                    for (final (d, _) in weekdays)
                      if (days[d]!.on) {'day': d, 'start': days[d]!.start, 'end': days[d]!.end},
                  ],
                };
                setState(() => (_busy = true, _error = null));
                try {
                  await widget.auth.transport.send('PUT', '${widget.path}/profile', body: body);
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

// --- tasks ---

/// Picks a photo for a task done with one (a test stands in for it).
typedef TaskPhotoPick = Future<({List<int> bytes, String name})?> Function();

TaskPhotoPick pickTaskPhoto = () async {
  final x = await stepOut(() => ImagePicker().pickImage(source: ImageSource.camera, imageQuality: 85, maxWidth: 2000));
  if (x == null) return null;
  return (bytes: await x.readAsBytes(), name: x.name.toLowerCase().endsWith('.png') ? x.name : '${x.name.split('.').first}.jpg');
};

/// Tasks: the person's own and, for those who give them, everyone's and
/// those that repeat.
class TasksPage extends StatefulWidget {
  const TasksPage({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<TasksPage> createState() => _TasksPageState();
}

class _TasksPageState extends State<TasksPage> with Polling {
  Map<String, dynamic>? _mine;
  Map<String, dynamic>? _all;
  bool _everyone = false;
  String? _error;
  String? _actError;
  bool _busy = false;
  String _said = '';

  Auth get _auth => widget.auth;
  DateTime get _now => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    try {
      final r = (await _auth.transport.send('GET', '/v1/staff/tasks', query: _everyone ? {'all': 'true'} : null) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_everyone ? _all = r : _mine = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null, _said = ''));
    try {
      await f();
      if (mounted) setState(() => _said = done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  String _path(Map<String, dynamic> t, [String rest = '']) => '/v1/staff/tasks/${Uri.encodeComponent(_str(t['id']))}$rest';

  List<ListStaffTasksResponsePeopleItem> get _people => [for (final p in _maps(_mine?['people'])) ListStaffTasksResponsePeopleItem.fromJson(p)];

  Future<void> _give() async {
    final given = await showTkModal<bool>(
      context,
      title: 'Give a task',
      builder: (_) => GiveTask(auth: _auth, people: _people, now: widget.now),
    );
    if (given == true && mounted) {
      setState(() => _said = 'Task given. They hear in their chat.');
      await _load();
    }
  }

  Future<void> _done(Map<String, dynamic> t) async {
    if (t['photo'] != true) return _run(() => _auth.transport.send('POST', _path(t, '/done'), body: const {}), 'Done. Thank you.');
    final photo = await pickTaskPhoto();
    if (photo == null || !mounted) return;
    await _run(
      () => _auth.transport.upload(
        _path(t, '/done'),
        bytes: photo.bytes,
        filename: photo.name,
        type: photo.name.endsWith('.png') ? 'image/png' : 'image/jpeg',
        field: 'photo',
      ),
      'Done, with the photo. Thank you.',
    );
  }

  Future<void> _cant(Map<String, dynamic> t) async {
    final why = await showTkModal<String>(
      context,
      title: 'Can’t do it',
      builder: (_) => _Why(task: t),
    );
    if (why == null) return;
    await _run(() => _auth.transport.send('POST', _path(t, '/cant'), body: {'why': why}), 'Sent back to ${t['giver']}');
  }

  Future<void> _pass(Map<String, dynamic> t) async {
    final who = await showTkModalChoices(context, title: 'Who it is for now', value: '', choices: whoChoices(_people));
    if (who == null || who.isEmpty) return;
    final i = who.indexOf(':');
    final kind = who.substring(0, i), id = who.substring(i + 1);
    await _run(
      () => _auth.transport.send('POST', _path(t, '/give'), body: kind == 'user' ? {'to_user': id} : {'to_role': id}),
      'Given. They hear in their chat.',
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final give = _mine?['give'] == true;
    final v = _everyone ? _all : _mine;
    final tasks = v == null ? const <Map<String, dynamic>>[] : _maps(v['tasks']);
    final repeats = v == null ? const <Map<String, dynamic>>[] : _maps(v['repeats']);
    final now = _now;
    return TkPageScreen(
      title: 'Tasks',
      description: 'Things to do, given to a person or to anyone in a role. They hear in their chat and reply DONE when it is done.',
      onRefresh: _load,
      action: give ? TkSmallButton(label: 'Give', semantics: 'Give a task', primary: true, onPressed: _give) : null,
      children: [
        if (give)
          Wrap(
            spacing: 8,
            children: [
              for (final (all, label) in const [(false, 'Mine'), (true, 'Everyone’s')])
                Semantics(
                  selected: _everyone == all,
                  child: TkSmallButton(
                    label: label,
                    primary: _everyone == all,
                    onPressed: () {
                      if (_everyone == all) return;
                      setState(() => _everyone = all);
                      _load();
                    },
                  ),
                ),
            ],
          ),
        if (_said.isNotEmpty) _notice(t, _said),
        if (_error != null) TkErrorBox(_error!),
        if (_actError != null) TkErrorBox(_actError!),
        if (v == null && _error == null) const TkLoading(rows: 3, label: 'Loading the tasks…'),
        if (v != null && tasks.isEmpty)
          TkEmpty(
            icon: 'check',
            title: _everyone ? 'No tasks this week.' : 'You have no tasks',
            line: _everyone ? null : 'When someone gives you one, it shows here and in your chat.',
            action: give ? TkSmallButton(label: 'Give a task', primary: true, onPressed: _give) : null,
          ),
        if (tasks.isNotEmpty)
          TkPageCard(
            children: [
              for (final task in tasks)
                () {
                  final badge = taskBadge(task);
                  final open = task['state'] == 'open';
                  final free = open && _str(task['to_role']).isNotEmpty && _str(task['taken_by']).isEmpty;
                  final what = _str(task['what']);
                  final mine = !_everyone;
                  return Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        TkRow(
                          name: what,
                          icon: task['state'] == 'done' ? 'check' : 'clock',
                          tone: badge?.tone,
                          sub: taskSub(task, mine, now),
                          pill: badge == null ? null : TkPill(badge.text, tone: badge.tone),
                        ),
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: [
                            if (_str(task['photo_url']).isNotEmpty)
                              TkLink(
                                label: 'See the photo',
                                onTap: () => openOrSay(context, () => openInBrowser(Uri.parse(_str(task['photo_url']))), 'The photo could not be opened.'),
                              ),
                            if (mine && free)
                              TkSmallButton(
                                label: 'I’ll do it',
                                semantics: 'I’ll do it: $what',
                                onPressed: _busy ? null : () => _run(() => _auth.transport.send('POST', _path(task, '/take'), body: const {}), 'It is yours'),
                              ),
                            if (mine && open)
                              TkSmallButton(
                                label: task['photo'] == true ? 'Done, with a photo' : 'Done',
                                semantics: '${task['photo'] == true ? 'Done, with a photo' : 'Done'}: $what',
                                primary: true,
                                onPressed: _busy ? null : () => _done(task),
                              ),
                            if (mine && open) TkSmallButton(label: 'Can’t', semantics: 'Can’t: $what', onPressed: _busy ? null : () => _cant(task)),
                            if (!mine && open)
                              TkSmallButton(
                                label: 'Remind',
                                semantics: 'Remind ${taskWho(task)}: $what',
                                onPressed: _busy
                                    ? null
                                    : () => _run(() => _auth.transport.send('POST', _path(task, '/remind'), body: const {}), 'Reminded ${taskWho(task)}'),
                              ),
                            if (!mine && (open || task['state'] == 'cant'))
                              TkSmallButton(
                                label: 'Give to someone else',
                                semantics: 'Give to someone else: $what',
                                onPressed: _busy ? null : () => _pass(task),
                              ),
                            if (!mine && (open || task['state'] == 'cant'))
                              TkSmallButton(
                                label: 'Call off',
                                semantics: 'Call off: $what',
                                danger: true,
                                onPressed: _busy
                                    ? null
                                    : () async {
                                        final yes = await showTkConfirm(
                                          context,
                                          title: 'Call off “$what”?',
                                          body: '${taskWho(task)} hears it is called off.',
                                          ok: 'Call it off',
                                          cancel: 'Keep it',
                                          danger: true,
                                        );
                                        if (yes) await _run(() => _auth.transport.send('DELETE', _path(task)), 'Called off');
                                      },
                              ),
                          ],
                        ),
                      ],
                    ),
                  );
                }(),
            ],
          ),
        if (_everyone && repeats.isNotEmpty)
          TkPageCard(
            title: 'Repeating',
            children: [
              for (final r in repeats)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      TkRow(
                        name: _str(r['what']),
                        icon: 'loop',
                        sub: [
                          taskWho({...r, 'taken_by': null}),
                          repeatWords(r),
                          if (r['photo'] == true) 'photo when done',
                        ].join(' · '),
                      ),
                      Align(
                        alignment: Alignment.centerLeft,
                        child: TkSmallButton(
                          label: 'Stop',
                          semantics: 'Stop: ${r['what']}',
                          onPressed: _busy
                              ? null
                              : () async {
                                  final yes = await showTkConfirm(
                                    context,
                                    title: 'Stop “${r['what']}”?',
                                    body: 'Tasks it made stay.',
                                    ok: 'Stop it',
                                    cancel: 'Keep it',
                                    danger: true,
                                  );
                                  if (!yes) return;
                                  await _run(() => _auth.transport.send('DELETE', '/v1/staff/task-repeats/${Uri.encodeComponent(_str(r['id']))}'), 'Stopped');
                                },
                        ),
                      ),
                    ],
                  ),
                ),
            ],
          ),
      ],
    );
  }
}

/// Can't: why, sent back to who gave it.
class _Why extends StatefulWidget {
  const _Why({required this.task});
  final Map<String, dynamic> task;

  @override
  State<_Why> createState() => _WhyState();
}

class _WhyState extends State<_Why> {
  final _why = TextEditingController();

  @override
  void dispose() {
    _why.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('${widget.task['what']}. ${widget.task['giver']} hears that you can’t, and why.', style: tkText(size: 15, color: t.text)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Why',
          child: TkTextBox(controller: _why, max: 300, hint: 'No fuel'),
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(label: 'Send it back', primary: true, onPressed: () => Navigator.of(context).pop(_why.text.trim())),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}
