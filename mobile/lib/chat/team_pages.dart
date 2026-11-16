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
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show dayLabel;
import 'give_task.dart' show GiveTask, roleWords, weekdays, whoChoices;
import 'money_sheets.dart' show dateLabel;
import 'words.dart' show CardTone, naira;
import '../kit/icon.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';
import 'storefront_orders_page.dart' show groupedPhone, whatsappLink;

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

String _dayOf(DateTime t) => _day(t);

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
  List<Map<String, dynamic>> _tasks = const [];
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
    try {
      final x = (await _auth.transport.send('GET', '/v1/staff/tasks') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _tasks = _maps(x['tasks']));
    } on Object {
      // The Tasks tile counts none.
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
    final look = TkLook.of(context);
    final v = _view;
    final d = _day;
    final late = d == null ? const <Map<String, dynamic>>[] : _maps(d['late']);
    final shifts = d == null ? const <Map<String, dynamic>>[] : _maps(d['shifts']);
    final invites = v == null ? const <Map<String, dynamic>>[] : _maps(v['invites']);
    final people = v == null ? const <Map<String, dynamic>>[] : _maps(v['people']);
    // Each person's day: in now, been and gone, late, or off.
    ({String word, String under, Color dot, Color fill, Color ink, int order}) state(Map<String, dynamic> p) {
      final id = _str(p['user_id']);
      final s = shifts.where((x) => x['user_id'] == id).lastOrNull;
      final l = late.where((x) => x['user_id'] == id).firstOrNull;
      if (s != null && _str(s['out_at']).isEmpty) return (word: 'In', under: _hm(_str(s['in_at'])), dot: look.lime, fill: look.lime, ink: look.limeInk, order: 0);
      if (l != null) return (word: 'Late', under: 'late', dot: look.orange, fill: look.orange, ink: look.orangeInk, order: 1);
      if (s != null) return (word: 'Gone', under: 'out ${_hm(_str(s['out_at']))}', dot: look.teal, fill: look.teal, ink: look.tealInk, order: 2);
      return (word: 'Off', under: 'off', dot: t.muted.withValues(alpha: 0.5), fill: t.muted, ink: t.muted, order: 3);
    }
    final ordered = [...people]..sort((a, b) => state(a).order - state(b).order);
    final came = people.where((p) => shifts.any((x) => x['user_id'] == p['user_id'])).length;
    final tasks = _tasks;
    final open = tasks.where((x) => x['state'] == 'open').toList();
    final lateTasks = open.where((x) => x['late'] == true).length;
    final doneToday = tasks.where((x) => x['state'] == 'done' && _str(x['done_at']).isNotEmpty && _day0(_str(x['done_at']), _str(d?['day']))).length;
    String? phoneOf(String id) => people.where((p) => p['user_id'] == id).map((p) => _str(p['phone'])).where((x) => x.isNotEmpty).firstOrNull;
    final firstLate = late.firstOrNull;
    final latePhone = firstLate == null ? null : phoneOf(_str(firstLate['user_id']));
    return TkPageScreen(
      title: 'Team',
      titleSize: 30,
      onRefresh: _load,
      action: TkHeadButton(icon: 'b-plus', label: 'Add staff', primary: true, onTap: _add),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (_actError != null) TkErrorBox(_actError!),
        if (v == null && _error == null) const TkLoading(rows: 4, label: 'Loading your staff…'),
        if (v != null && people.isNotEmpty)
          TkTopCard(
            semanticsLabel: 'Today',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('In today', color: look.limeInk, trailing: d == null ? null : dayLabel(_str(d['day'])).split(' ').take(3).join(' ')),
                const SizedBox(height: 6),
                Text.rich(
                  TextSpan(
                    children: [
                      TextSpan(text: '$came', style: tkFigure(context, size: 40)),
                      TextSpan(text: ' of ${people.length}', style: tkDisplay(size: 22, color: t.muted)),
                    ],
                  ),
                ),
                Text('They said “in” to Taskiem on WhatsApp', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
                const SizedBox(height: 14),
                TkFaceRow(faces: [for (final p in ordered) (name: _str(p['name']).isNotEmpty ? _str(p['name']) : _str(p['email']), dot: state(p).dot, under: state(p).under)]),
                if (firstLate != null) ...[
                  const SizedBox(height: 14),
                  TkCallout(
                    leading: TkSquare('b-clock', color: look.orange),
                    title: late.length == 1 ? '${_str(firstLate['name'])} is late' : '${late.length} are late',
                    sub: late.length == 1 ? 'Due at ${firstLate['due']} · not in yet' : late.map((l) => '${l['name']} (${l['due']})').join(', '),
                    color: look.orange,
                    button: late.length == 1 && latePhone != null ? 'Call' : null,
                    semantics: 'Call ${_str(firstLate['name'])}',
                    onPressed: latePhone == null ? null : () => openOrSay(context, () => callPhone(latePhone), 'That number could not be called.'),
                  ),
                ],
              ],
            ),
          ),
        if (v != null)
          TkHighlights(
            tiles: [
              TkHighlight(
                label: 'Tasks',
                icon: 'b-check',
                color: look.blueInk,
                trailing: lateTasks > 0 ? '$lateTasks late' : null,
                value: '${open.length} open',
                line: '$doneToday done today',
                onTap: () => _go(TasksPage(auth: _auth, backLabel: 'Back to Team')),
                semanticsLabel: 'Tasks: ${open.length} open${lateTasks > 0 ? ', $lateTasks late' : ''}',
              ),
              TkHighlight(
                label: 'Invited',
                icon: 'b-chat',
                color: look.violetInk,
                value: '${invites.length} waiting',
                line: 'to join on WhatsApp',
              ),
            ],
          ),
        if (v != null)
          TkSection(
            title: 'Your staff',
            trailing: Text('${people.length}', style: tkText(size: 14, weight: 600, color: t.muted)),
            empty: 'No one yet. Add the people who work with you.',
            children: [
              for (final p in ordered)
                () {
                  final name = _str(p['name']).isNotEmpty ? _str(p['name']) : _str(p['email']);
                  final s = state(p);
                  final id = _str(p['user_id']);
                  final shift = shifts.where((x) => x['user_id'] == id).lastOrNull;
                  final due = late.where((x) => x['user_id'] == id).firstOrNull;
                  return TkListRow(
                    title: '$name${p['you'] == true ? ' (you)' : ''}',
                    letters: name,
                    sub: [
                      _strs(p['roles']).map(roleWords).join(', '),
                      if (s.word == 'In' && shift != null) 'in since ${_hm(_str(shift['in_at']))}',
                      if (s.word == 'Gone' && shift != null) shiftWords(shift).toLowerCase(),
                      if (s.word == 'Late' && due != null) 'due at ${due['due']}',
                      if (s.word == 'Off') 'off today',
                      if (p['chat'] != true) 'web only',
                    ].where((x) => x.isNotEmpty).join(' · '),
                    status: s.word,
                    statusAccent: (fill: s.fill, ink: s.ink),
                    waiting: s.word == 'Late',
                    onTap: () => _go(StaffPersonPage(auth: _auth, user: id, canRemove: p['you'] != true)),
                  );
                }(),
            ],
          ),
        if (invites.isNotEmpty)
          TkSection(
            title: 'Waiting to join',
            children: [
              for (final x in invites)
                () {
                  final who = _str(x['name']).isNotEmpty ? _str(x['name']) : _str(x['phone']);
                  final left = DateTime.tryParse(_str(x['expires_at']))?.difference(DateTime.now()).inDays;
                  return Row(
                    children: [
                      Expanded(
                        child: TkListRow(
                          title: who,
                          letters: who,
                          sub: [
                            _strs(x['roles']).map(roleWords).join(', '),
                            if (_str(x['inviter']).isNotEmpty) 'invited by ${x['inviter']}',
                            if (left != null && left >= 0) '${left == 1 ? '1 day' : '$left days'} left' else if (_str(x['sent_at']).isEmpty) 'not sent yet',
                          ].where((s) => s.isNotEmpty).join(' · '),
                          onTap: _busy ? null : () => _invite(x, who),
                          semanticsLabel: '$who, waiting to join',
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 12),
                        child: TkSmallButton(label: 'Send again', semantics: 'Send again to $who', onPressed: _busy ? null : () => _sendAgain(x, who)),
                      ),
                    ],
                  );
                }(),
            ],
          ),
      ],
    );
  }

  /// Whether a time falls on [day] (the clock's today), else today.
  bool _day0(String iso, String day) {
    final d = DateTime.tryParse(iso);
    return d != null && _dayOf(d) == (day.isNotEmpty ? day : _dayOf(DateTime.now()));
  }

  Future<void> _sendAgain(Map<String, dynamic> x, String who) => _run(() async {
    final r = (await _auth.transport.send('POST', '/v1/staff/invites/${Uri.encodeComponent(_str(x['id']))}/send', body: const {}) as Map?) ?? const {};
    if (r['sent'] != true) throw StateError('The WhatsApp message did not go. Try again in a little while.');
    return 'Sent again to $who';
  });

  /// An invitation pressed: send it again, or cancel it after yes.
  Future<void> _invite(Map<String, dynamic> x, String who) async {
    final pick = await showTkModalChoices(context, title: who, choices: [('send', 'Send again'), ('cancel', 'Cancel the invitation')]);
    if (!mounted) return;
    if (pick == 'send') return _sendAgain(x, who);
    if (pick != 'cancel') return;
    final yes = await showTkConfirm(context, title: 'Cancel the invitation to $who?', body: 'Join will no longer work.', ok: 'Cancel it', cancel: 'Keep it', danger: true);
    if (!yes) return;
    await _run(() async {
      await _auth.transport.send('DELETE', '/v1/staff/invites/${Uri.encodeComponent(_str(x['id']))}');
      return 'Invitation cancelled';
    });
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
  const StaffPersonPage({super.key, required this.auth, required this.user, this.canRemove = false});
  final Auth auth;
  final String user;

  /// Someone other than the person looking: Remove from the business.
  final bool canRemove;

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

  Future<void> _change(String name, Map<String, dynamic> pr) async {
    final saved = await showTkModal<bool>(context, title: 'Change $name', builder: (_) => _EditProfile(auth: widget.auth, path: _path, profile: pr));
    if (saved == true && mounted) {
      showTkToast(context, 'Saved');
      await _load();
    }
  }

  Future<void> _remove(Map<String, dynamic> p, String name) async {
    final yes = await showTkConfirm(
      context,
      title: 'Remove $name from the business?',
      body: 'They lose ${_strs(p['roles']).map(roleWords).join(', ')}; what they did stays.',
      ok: 'Remove',
      cancel: 'Keep them',
      danger: true,
    );
    if (!yes || !mounted) return;
    try {
      await widget.auth.transport.send('POST', '$_path/remove', body: const {});
      if (!mounted) return;
      showTkToast(context, '$name removed');
      Navigator.of(context).maybePop();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _p;
    final name = p == null ? '' : (_str(p['name']).isNotEmpty ? _str(p['name']) : (_str(p['email']).isNotEmpty ? _str(p['email']) : 'Someone'));
    final pr = p == null ? const <String, dynamic>{} : _map(p['profile']);
    final f = _figures;
    final phone = p == null ? '' : _str(p['phone']);
    final wa = whatsappLink(phone);
    final job = _str(pr['title']).isNotEmpty ? _str(pr['title']) : (p == null ? '' : _strs(p['roles']).map(roleWords).join(', '));
    return TkPageScreen(
      title: p == null ? 'Staff' : name,
      titleSize: 22,
      backLabel: 'Back to Team',
      onRefresh: () async {
        await _load();
        await _loadFigures();
      },
      action: p == null ? null : TkHeadButton(icon: 'b-more', label: 'More about $name', onTap: () async {
        final pick = await showTkModalChoices(context, title: name, choices: [('change', 'Change their job, hours and pay')]);
        if (pick == 'change' && mounted) await _change(name, pr);
      }),
      under: TkChips<String>(
        label: 'Show',
        chips: const [('day', 'Today'), ('week', 'This week'), ('month', 'This month')],
        on: _period,
        onPick: (v) {
          if (_period == v) return;
          setState(() => _period = v);
          _loadFigures();
        },
      ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 4, label: 'Loading…'),
        if (_figuresError != null) TkErrorBox(_figuresError!),
        if (p != null && f == null && _figuresError == null) const TkLoading(rows: 3, label: 'Loading their figures…'),
        if (p != null && f != null) ..._figuresCards(t, look, f),
        if (p != null)
          TkPersonCard(
            name: name,
            sub: [if (phone.isNotEmpty) groupedPhone(phone) else _str(p['email']), job].where((x) => x.isNotEmpty).join(' · '),
            actions: [
              if (phone.isNotEmpty) (icon: 'b-phone', label: 'Call', semantics: 'Call $name', onTap: () => openOrSay(context, () => callPhone(phone), 'That number could not be called.')),
              if (wa != null) (icon: 'b-chat', label: 'WhatsApp', semantics: 'WhatsApp $name', onTap: () => openOrSay(context, () => openOutside(Uri.parse(wa)), 'WhatsApp could not be opened.')),
            ],
          ),
        if (p != null)
          TkSection(
            title: 'Their job',
            inset: 56,
            children: [
              TkListRow(
                title: job.isEmpty ? 'What they do' : job,
                sub: [
                  if (_str(pr['started_on']).isNotEmpty) 'Since ${dateLabel(_str(pr['started_on']))}',
                  p['chat'] == true ? 'on chat' : 'web only',
                ].join(' · '),
                icon: 'b-people',
                square: look.teal,
                onTap: () => _change(name, pr),
              ),
              TkListRow(title: 'Hours', sub: rotaWords(_maps(pr['rota'])), icon: 'b-clock', square: look.blue, onTap: () => _change(name, pr)),
              TkListRow(
                title: 'Pay',
                sub: payWords(pr).isNotEmpty ? '${payWords(pr)} · for your records' : 'Not set · for your records',
                icon: 'b-cash',
                square: look.lime,
                onTap: () => _change(name, pr),
              ),
              if (_str(pr['note']).isNotEmpty) TkListRow(title: 'Note', sub: _str(pr['note']), icon: 'b-note', square: look.violet, onTap: () => _change(name, pr)),
            ],
          ),
        if (p != null && widget.canRemove)
          TkSection(
            inset: 56,
            children: [TkListRow(title: 'Remove from the business', icon: 'b-x', square: look.red, semanticsLabel: 'Remove $name', onTap: () => _remove(p, name))],
          ),
      ],
    );
  }

  /// The top card (hours, time keeping, the week's days) and the tiles
  /// (sales, tasks, the till at handover).
  List<Widget> _figuresCards(TkTokens t, TkLook look, Map<String, dynamic> f) {
    final minutes = _int(f['minutes']);
    final shifts = _int(f['shifts']);
    final lateDays = _strs(f['late_days']);
    final missed = _strs(f['missed_days']);
    String count(int n, String one) => '$n $one${n == 1 ? '' : 's'}';
    final keeping = [
      count(shifts, 'shift'),
      lateDays.isEmpty ? 'never late' : 'late ${lateDays.length == 1 ? 'once' : '${lateDays.length} times'}',
      missed.isEmpty ? 'no days missed' : '${count(missed.length, 'day')} missed',
    ].join(' · ');
    // This week's days, Monday first, marking the late and missed ones.
    final now = _lagos(DateTime.now());
    final monday = DateTime.utc(now.year, now.month, now.day).subtract(Duration(days: now.weekday - 1));
    String key(DateTime d) => '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
    final sales = _map(f['sales']);
    final hand = _maps(f['handovers']).where((h) => h['diff_kobo'] != null).toList();
    final diff = hand.fold(0, (n, h) => n + _int(h['diff_kobo']));
    final tk = _map(f['tasks']);
    return [
      TkTopCard(
        semanticsLabel: 'Hours worked',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow('Hours worked', color: look.tealInk, trailing: switch (_period) { 'day' => 'today', 'month' => 'this month', _ => 'this week' }),
            const SizedBox(height: 6),
            FittedBox(
              fit: BoxFit.scaleDown,
              alignment: Alignment.centerLeft,
              child: Text(shifts == 0 ? 'None' : '${minutes ~/ 60}h ${(minutes % 60).toString().padLeft(2, '0')}m', style: tkFigure(context, size: 40)),
            ),
            Text(shifts == 0 ? 'No shifts clocked' : keeping, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            if (_period == 'week') ...[
              const SizedBox(height: 14),
              TkDayMarks(
                days: [
                  for (var i = 0; i < 7; i++)
                    () {
                      final k = key(monday.add(Duration(days: i)));
                      final l = lateDays.contains(k), m = missed.contains(k);
                      return (letter: const ['M', 'T', 'W', 'T', 'F', 'S', 'S'][i], word: l ? 'late' : (m ? 'missed' : ''), color: l ? look.orange : (m ? look.red : null));
                    }(),
                ],
              ),
            ],
          ],
        ),
      ),
      TkStatTiles([
        (
          label: 'Sales',
          ink: look.limeInk,
          value: _int(sales['count']) == 0 ? 'None' : naira(_int(sales['total_kobo'])),
          line: [if (_int(sales['count']) > 0) count(_int(sales['count']), 'sale'), if (_int(f['orders']) > 0) count(_int(f['orders']), 'order')].join(' · ').ifEmpty('none recorded'),
        ),
        (
          label: 'Tasks',
          ink: look.blueInk,
          value: '${_int(tk['done'])} done',
          line: [
            if (_int(tk['done_late']) > 0) '${_int(tk['done_late'])} late',
            if (_int(tk['open']) > 0) '${_int(tk['open'])} open',
            if (_int(tk['cant']) > 0) '${_int(tk['cant'])} sent back',
          ].join(' · ').ifEmpty('none open'),
        ),
        (
          label: 'Till',
          ink: diff < 0 ? look.orangeInk : look.tealInk,
          value: hand.isEmpty ? '—' : (diff == 0 ? 'Right' : naira(diff.abs())),
          line: hand.isEmpty ? 'no handover' : (diff < 0 ? 'short at handover' : (diff > 0 ? 'over at handover' : count(hand.length, 'handover'))),
        ),
      ]),
    ];
  }
}

extension on String {
  String ifEmpty(String other) => isEmpty ? other : this;
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
  const TasksPage({super.key, required this.auth, this.now, this.backLabel = 'Back'});
  final Auth auth;

  /// Where Back goes, said ("Back to Team").
  final String backLabel;
  final DateTime Function()? now;

  @override
  State<TasksPage> createState() => _TasksPageState();
}

class _TasksPageState extends State<TasksPage> with Polling {
  Map<String, dynamic>? _mine;
  Map<String, dynamic>? _all;
  bool _everyone = false;

  /// The repeating tasks shown in the list's place.
  bool _repeating = false;
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
    final given = await showTkSheet<bool>(context, (_) => GiveTask(auth: _auth, people: _people, now: widget.now, title: 'Give a task'));
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

  /// A task pressed: what else can be done to it.
  Future<void> _more(Map<String, dynamic> task) async {
    final what = _str(task['what']);
    final open = task['state'] == 'open';
    final mine = !_everyone;
    final free = open && _str(task['to_role']).isNotEmpty && _str(task['taken_by']).isEmpty;
    final choices = [
      if (_str(task['photo_url']).isNotEmpty) ('photo', 'See the photo'),
      // A role's task shows I'll do it; done straight away from here.
      if (mine && free) ('done', task['photo'] == true ? 'Done, with a photo' : 'Done'),
      if (mine && open) ('cant', 'Can’t do it'),
      if (!mine && open) ('remind', 'Remind ${taskWho(task)}'),
      if (!mine && (open || task['state'] == 'cant')) ('pass', 'Give to someone else'),
      if (!mine && (open || task['state'] == 'cant')) ('off', 'Call it off'),
    ];
    if (choices.isEmpty) return;
    final pick = await showTkModalChoices(context, title: what, choices: choices);
    if (!mounted) return;
    switch (pick) {
      case 'photo':
        await openOrSay(context, () => openInBrowser(Uri.parse(_str(task['photo_url']))), 'The photo could not be opened.');
      case 'done':
        await _done(task);
      case 'cant':
        await _cant(task);
      case 'remind':
        await _run(() => _auth.transport.send('POST', _path(task, '/remind'), body: const {}), 'Reminded ${taskWho(task)}');
      case 'pass':
        await _pass(task);
      case 'off':
        final yes = await showTkConfirm(context, title: 'Call off “$what”?', body: '${taskWho(task)} hears it is called off.', ok: 'Call it off', cancel: 'Keep it', danger: true);
        if (yes) await _run(() => _auth.transport.send('DELETE', _path(task)), 'Called off');
    }
  }

  Widget _row(TkTokens t, TkLook look, Map<String, dynamic> task, DateTime now) {
    final what = _str(task['what']);
    final state = _str(task['state']);
    final open = state == 'open';
    final mine = !_everyone;
    final free = open && _str(task['to_role']).isNotEmpty && _str(task['taken_by']).isEmpty;
    final late = open && task['late'] == true;
    final done = state == 'done';
    final badge = taskBadge(task);
    final Widget? button = mine && free
        ? TkSmallButton(
            label: 'I’ll do it',
            semantics: 'I’ll do it: $what',
            onPressed: _busy ? null : () => _run(() => _auth.transport.send('POST', _path(task, '/take'), body: const {}), 'It is yours'),
          )
        : mine && open
        ? TkSmallButton(
            label: task['photo'] == true ? 'Done, with a photo' : 'Done',
            semantics: '${task['photo'] == true ? 'Done, with a photo' : 'Done'}: $what',
            primary: true,
            onPressed: _busy ? null : () => _done(task),
          )
        : !mine && late
        ? TkSmallButton(
            label: 'Remind',
            semantics: 'Remind ${taskWho(task)}: $what',
            onPressed: _busy ? null : () => _run(() => _auth.transport.send('POST', _path(task, '/remind'), body: const {}), 'Reminded ${taskWho(task)}'),
          )
        : null;
    final (pillFill, pillInk) = switch (badge?.tone) {
      CardTone.warn => (look.orange, look.orangeInk),
      CardTone.danger => (look.red, look.redInk),
      CardTone.lime => (look.lime, look.limeInk),
      _ => (t.muted, t.muted),
    };
    return TkPress(
      onTap: () => _more(task),
      scale: 0.985,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 11, 12, 11),
        child: Row(
          children: [
            ExcludeSemantics(
              child: Container(
                width: 24,
                height: 24,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  color: done ? look.lime : null,
                  border: done ? null : Border.all(color: late ? look.orange : t.muted, width: 2),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: done ? const TkIcon('b-check', size: 15, stroke: 2.6, color: Color(0xFFFFFFFF)) : null,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: MergeSemantics(
                child: Semantics(
                  button: true,
                  onTap: () => _more(task),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        what,
                        style: tkText(size: 15.5, weight: 500, lineHeight: 1.3, color: done ? t.muted : t.text, decoration: done ? TextDecoration.lineThrough : null),
                      ),
                      Text(taskSub(task, mine, now), style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
                    ],
                  ),
                ),
              ),
            ),
            if (badge != null && !done || button != null) ...[
              const SizedBox(width: 8),
              Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  if (badge != null && !done)
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 2),
                      decoration: BoxDecoration(color: pillFill.withValues(alpha: 0.16), borderRadius: BorderRadius.circular(999)),
                      child: Text(badge.text, style: tkText(size: 11.5, weight: 700, color: pillInk)),
                    ),
                  if (badge != null && !done && button != null) const SizedBox(height: 6),
                  ?button,
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final give = _mine?['give'] == true;
    final v = _everyone ? _all : _mine;
    final tasks = v == null ? const <Map<String, dynamic>>[] : _maps(v['tasks']);
    final repeats = _maps((_all ?? _mine)?['repeats']);
    final now = _now;
    final late = tasks.where((x) => x['state'] == 'open' && x['late'] == true).toList();
    final open = tasks.where((x) => x['state'] == 'open' && x['late'] != true).toList();
    final done = tasks.where((x) => x['state'] == 'done').toList();
    final back = tasks.where((x) => x['state'] == 'cant' || x['state'] == 'cancelled').toList();
    final counted = late.length + open.length + done.length;
    final mineCount = _maps(_mine?['tasks']).where((x) => x['state'] == 'open').length;
    final allCount = _all == null ? null : _maps(_all!['tasks']).where((x) => x['state'] == 'open').length;
    return TkPageScreen(
      title: 'Tasks',
      titleSize: 30,
      backLabel: widget.backLabel,
      onRefresh: _load,
      action: give ? TkHeadButton(icon: 'b-plus', label: 'Give a task', primary: true, onTap: _give) : null,
      under: give
          ? TkChips<String>(
              label: 'Show',
              chips: [('mine', 'Yours · $mineCount'), ('all', allCount == null ? 'Everyone’s' : 'Everyone’s · $allCount'), ('repeat', 'Repeating')],
              on: _repeating ? 'repeat' : (_everyone ? 'all' : 'mine'),
              onPick: (k) {
                final all = k != 'mine';
                setState(() => (_repeating = k == 'repeat', _everyone = all));
                _load();
              },
            )
          : null,
      children: [
        if (_said.isNotEmpty) _notice(t, _said),
        if (_error != null) TkErrorBox(_error!),
        if (_actError != null) TkErrorBox(_actError!),
        if (v == null && _error == null) const TkLoading(rows: 3, label: 'Loading the tasks…'),
        if (_repeating) ...[
          if (v != null)
            TkSection(
              inset: 56,
              empty: 'Nothing repeats. Give a task and choose how often.',
              children: [
                for (final r in repeats)
                  Row(
                    children: [
                      Expanded(
                        child: TkListRow(
                          title: _str(r['what']),
                          sub: [taskWho({...r, 'taken_by': null}), repeatWords(r), if (r['photo'] == true) 'photo when done'].join(' · '),
                          icon: 'b-flow',
                          square: look.violet,
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 12),
                        child: TkSmallButton(
                          label: 'Stop',
                          semantics: 'Stop: ${r['what']}',
                          onPressed: _busy
                              ? null
                              : () async {
                                  final yes = await showTkConfirm(context, title: 'Stop “${r['what']}”?', body: 'Tasks it made stay.', ok: 'Stop it', cancel: 'Keep it', danger: true);
                                  if (!yes) return;
                                  await _run(() => _auth.transport.send('DELETE', '/v1/staff/task-repeats/${Uri.encodeComponent(_str(r['id']))}'), 'Stopped');
                                },
                        ),
                      ),
                    ],
                  ),
              ],
            ),
        ] else ...[
          if (v != null && tasks.isEmpty)
            TkEmpty(
              icon: 'check',
              title: _everyone ? 'No tasks this week.' : 'You have no tasks',
              line: _everyone ? null : 'When someone gives you one, it shows here and in your chat.',
              action: give ? TkSmallButton(label: 'Give a task', primary: true, onPressed: _give) : null,
            ),
          if (counted > 0)
            TkTopCard(
              semanticsLabel: 'Tasks: ${done.length} done, ${open.length} open, ${late.length} late',
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkEyebrow(_everyone ? 'Everyone’s' : 'Yours', color: look.blueInk, trailing: '$counted given'),
                  const SizedBox(height: 6),
                  Text.rich(
                    TextSpan(
                      children: [
                        TextSpan(text: '${done.length}', style: tkFigure(context, size: 40)),
                        TextSpan(text: ' of $counted done', style: tkDisplay(size: 22, color: t.muted)),
                      ],
                    ),
                  ),
                  const SizedBox(height: 12),
                  TkSplitBar(
                    percent: false,
                    parts: [
                      (label: 'Done ${done.length}', share: done.length, color: look.lime),
                      (label: 'Open ${open.length}', share: open.length, color: look.blue),
                      (label: 'Late ${late.length}', share: late.length, color: look.orange),
                    ],
                  ),
                ],
              ),
            ),
          if (late.isNotEmpty) TkSection(title: 'Late', count: late.length, inset: 50, children: [for (final x in late) _row(t, look, x, now)]),
          if (open.isNotEmpty) TkSection(title: 'Open', inset: 50, children: [for (final x in open) _row(t, look, x, now)]),
          if (back.isNotEmpty) TkSection(title: 'Sent back or called off', inset: 50, children: [for (final x in back) _row(t, look, x, now)]),
          if (done.isNotEmpty) TkSection(title: 'Done', inset: 50, children: [for (final x in done) _row(t, look, x, now)]),
        ],
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
