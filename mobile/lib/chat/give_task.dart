// "Give this to…" on a card (web/src/pages/Tasks.tsx GiveThis, GiveTask,
// WhoSelect; lib/tasks.ts): for those who give tasks, a task about the
// thing on the card — what is to be done, already said; who it is for; when;
// whether a photo is wanted when it is done.

import 'package:flutter/material.dart' show showDatePicker, showTimePicker, TimeOfDay, Material, MaterialType;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/look.dart' show TkFace;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../kit/tap_area.dart';
import '../theme/look.dart' show TkLook, tkFaceFor;
import '../kit/forms.dart' show TkSwitch;
import 'approve.dart' show TkTextBox;
import '../theme/theme.dart';
import 'words.dart' show dayKey;

/// tasks.ts TaskLink: what a task is about.
typedef TaskLink = ({String kind, String id, String? name});

/// tasks.ts linkedWhat: what a card's "Give this to…" starts the task with.
String linkedWhat(TaskLink link) => switch (link.kind) {
  'order' => 'Pack order ${link.name ?? link.id}',
  'invoice' => 'Follow up invoice ${link.name ?? link.id}',
  'bill' => 'Check bill ${link.name ?? link.id}',
  'purchase' => 'Receive order ${link.name ?? link.id}',
  'stock' => 'Restock ${link.name ?? 'this item'}',
  _ => '',
};

/// team.ts roleWords: shift_manager is "Shift manager".
String roleWords(String role) {
  const named = {
    'owner': 'Owner',
    'admin': 'Admin',
    'cashier': 'Cashier',
    'shift_manager': 'Shift manager',
    'viewer': 'Viewer',
    'operator': 'Operator',
    'approver': 'Approver',
    'builder': 'Builder',
    'auditor': 'Auditor',
  };
  if (named[role] case final w?) return w;
  if (role.isEmpty) return role;
  return role[0].toUpperCase() + role.substring(1).replaceAll('_', ' ');
}

/// team.ts WEEKDAYS.
const weekdays = [
  ('mon', 'Monday'),
  ('tue', 'Tuesday'),
  ('wed', 'Wednesday'),
  ('thu', 'Thursday'),
  ('fri', 'Friday'),
  ('sat', 'Saturday'),
  ('sun', 'Sunday'),
];

/// tasks.ts TaskForm.
class TaskForm {
  String what = '';
  String who = '';
  String when = 'none';
  String date;
  String time = '';
  String day = 'mon';
  bool photo = false;
  TaskForm(this.date);
}

/// tasks.ts taskProblem.
String? taskProblem(TaskForm f) {
  final what = f.what.trim();
  if (what.isEmpty) return 'Say what is to be done.';
  if (what.length > 200) return 'Keep what is to be done to 200 letters.';
  if (f.who.isEmpty) return 'Choose who it is for.';
  if (f.when != 'none' && !RegExp(r'^([01]\d|2[0-3]):[0-5]\d$').hasMatch(f.time)) return 'Choose the time it is due.';
  if (f.when == 'by' && !RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(f.date)) return 'Choose the day it is due.';
  return null;
}

/// tasks.ts lagosTime: a day and an HH:MM in Lagos (UTC+1, no summer time).
DateTime lagosTime(String date, String time) => DateTime.parse('${date}T$time:00+01:00').toUtc();

/// tasks.ts taskRequest.
StaffTaskIn taskRequest(TaskForm f, TaskLink? link) {
  final i = f.who.indexOf(':');
  final kind = f.who.substring(0, i), id = f.who.substring(i + 1);
  return StaffTaskIn(
    what: f.what.trim(),
    photo: f.photo,
    toUser: kind == 'user' ? id : null,
    toRole: kind == 'user' ? null : id,
    dueAt: f.when == 'by' ? lagosTime(f.date, f.time) : null,
    repeat: switch (f.when) {
      'day' => StaffTaskInRepeat(every: 'day', at: f.time),
      'week' => StaffTaskInRepeat(every: 'week', day: f.day, at: f.time),
      _ => null,
    },
    link: link == null ? null : StaffTaskInLink(kind: link.kind, id: link.id, name: link.name),
  );
}

/// Tasks.tsx WhoSelect's choices: each person, then anyone in a role (not
/// the owner's).
List<(String, String)> whoChoices(List<ListStaffTasksResponsePeopleItem> people) {
  final roles = {for (final p in people) ...p.roles}.where((r) => r != 'owner').toList()..sort();
  return [
    for (final p in people) ('user:${p.id}', p.name),
    for (final r in roles) ('role:$r', 'Any ${roleWords(r).toLowerCase()}'),
  ];
}

/// GiveThis's press: the people, then the form as a sheet; a word when it
/// is given.
Future<void> giveThis(BuildContext context, Auth auth, TaskLink link) async {
  final List<ListStaffTasksResponsePeopleItem> people;
  try {
    people = (await auth.api.listStaffTasks()).people;
  } on ApiError catch (e) {
    if (context.mounted) showTkToast(context, e.message, error: true);
    return;
  } on Offline {
    if (context.mounted) showTkToast(context, 'You’re offline. Try again when you’re back online.', error: true);
    return;
  }
  if (!context.mounted) return;
  final given = await showTkSheet<bool>(context, (_) => GiveTask(auth: auth, people: people, link: link));
  if (given == true && context.mounted) showTkToast(context, 'Task given. They hear in their chat.');
}

/// Tasks.tsx GiveTask, as drawn: a sheet. What is to be done; who it is for
/// as faces, then anyone in a role as chips; when, as a switch; the day and
/// time to pick; a photo as a switch. It pops true once the task is given.
class GiveTask extends StatefulWidget {
  const GiveTask({super.key, required this.auth, required this.people, this.link, this.now, this.title = 'Give this to someone'});
  final Auth auth;
  final List<ListStaffTasksResponsePeopleItem> people;
  final TaskLink? link;
  final DateTime Function()? now;
  final String title;

  @override
  State<GiveTask> createState() => _GiveTaskState();
}

class _GiveTaskState extends State<GiveTask> {
  late final TaskForm _f = TaskForm(dayKey((widget.now ?? DateTime.now)()))..what = widget.link == null ? '' : linkedWhat(widget.link!);
  late final _what = TextEditingController(text: _f.what);
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _what.addListener(() => setState(() => _f.what = _what.text));
  }

  @override
  void dispose() {
    _what.dispose();
    super.dispose();
  }

  Future<void> _give() async {
    setState(() => _tried = true);
    if (taskProblem(_f) != null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.auth.api.giveStaffTask(body: taskRequest(_f, widget.link));
      if (mounted) Navigator.of(context).pop(true);
      return;
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _pickDay() async {
    final now = (widget.now ?? DateTime.now)();
    final had = DateTime.tryParse(_f.date) ?? now;
    final d = await showDatePicker(context: context, initialDate: had, firstDate: DateTime(now.year - 1), lastDate: DateTime(now.year + 5));
    if (d != null) setState(() => _f.date = dayKey(d));
  }

  Future<void> _pickTime() async {
    final m = RegExp(r'^(\d\d):(\d\d)$').firstMatch(_f.time);
    final had = m == null ? const TimeOfDay(hour: 9, minute: 0) : TimeOfDay(hour: int.parse(m[1]!), minute: int.parse(m[2]!));
    final t = await showTimePicker(context: context, initialTime: had);
    if (t != null) setState(() => _f.time = '${'${t.hour}'.padLeft(2, '0')}:${'${t.minute}'.padLeft(2, '0')}');
  }

  /// "Fri 13 Nov" for a picked day.
  String _dayWords(String key) {
    final d = DateTime.tryParse(key);
    if (d == null) return key;
    const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    return '${days[d.weekday - 1]} ${d.day} ${months[d.month - 1]}';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = taskProblem(_f);
    final f = _f;
    final choices = whoChoices(widget.people);
    final people = [for (final c in choices) if (c.$1.startsWith('user:')) c];
    final roles = [for (final c in choices) if (c.$1.startsWith('role:')) c];
    return TkFormSheet(
      title: widget.title,
      action: _busy ? 'Giving…' : 'Give the task',
      onAction: _busy ? null : _give,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(false),
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'What is to be done', child: TkTextBox(controller: _what, max: 200, size: 15, height: 46)),
            TkKitField(
              label: 'Who it is for',
              field: false,
              hint: 'For anyone in a role, the first to take it has it.',
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (people.isNotEmpty)
                    SingleChildScrollView(
                      scrollDirection: Axis.horizontal,
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final (v, name) in people) _WhoFace(name: name, on: f.who == v, onTap: () => setState(() => f.who = v)),
                        ],
                      ),
                    ),
                  if (roles.isNotEmpty) ...[
                    const SizedBox(height: 10),
                    Wrap(
                      spacing: 6,
                      runSpacing: 6,
                      children: [for (final (v, words) in roles) TkPillChoice(label: words, on: f.who == v, onTap: () => setState(() => f.who = v))],
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
        TkChips<String>(
          label: 'When',
          padding: EdgeInsets.zero,
          chips: const [('none', 'Any time'), ('by', 'By a time'), ('day', 'Daily'), ('week', 'Weekly')],
          on: f.when,
          onPick: (v) => setState(() => f.when = v),
        ),
        if (f.when != 'none')
          TkFormGroup(
            children: [
              if (f.when == 'week')
                TkKitField(
                  label: 'On',
                  field: false,
                  child: Wrap(
                    spacing: 6,
                    runSpacing: 6,
                    children: [for (final (v, day) in weekdays) TkPillChoice(label: day.substring(0, 3), semantics: day, on: f.day == v, onTap: () => setState(() => f.day = v))],
                  ),
                ),
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (f.when == 'by') ...[
                    Expanded(child: TkKitField(label: 'Day', field: false, child: TkPickBox(label: 'Day', shown: _dayWords(f.date), icon: 'b-calendar', onTap: _pickDay))),
                    const SizedBox(width: 10),
                  ],
                  Expanded(
                    child: TkKitField(
                      label: f.when == 'by' ? 'By' : 'Due at',
                      field: false,
                      child: TkPickBox(label: f.when == 'by' ? 'By' : 'Due at', shown: f.time.isEmpty ? '--:--' : f.time, icon: 'b-clock', onTap: _pickTime),
                    ),
                  ),
                ],
              ),
            ],
          ),
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'A photo when it is done',
              icon: 'b-camera',
              square: TkLook.of(context).violet,
              onTap: () => setState(() => f.photo = !f.photo),
              trailing: TkSwitch(label: 'A photo when it is done', on: f.photo, onChanged: (v) => setState(() => f.photo = v)),
            ),
          ],
        ),
        if (_tried && problem != null) Semantics(liveRegion: true, child: Text(problem, style: tkText(size: 14, color: t.danger))),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

/// A person to give it to, as a face with their first name (as drawn).
class _WhoFace extends StatelessWidget {
  const _WhoFace({required this.name, required this.on, required this.onTap});
  final String name;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final ring = dark ? t.brandDark : t.brand;
    return Semantics(
      button: true,
      selected: on,
      label: name,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: SizedBox(
          width: 64,
          child: Column(
            children: [
              Container(
                padding: const EdgeInsets.all(2),
                decoration: BoxDecoration(shape: BoxShape.circle, border: Border.all(color: on ? ring : const Color(0x00000000), width: 2)),
                child: TkFace(tkInitials(name), size: 40, colors: tkFaceFor(name)),
              ),
              const SizedBox(height: 4),
              Text(
                name.split(' ').first,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: tkText(size: 12, weight: on ? 700 : 500, color: on ? t.text : t.muted),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A choice as a small pill, pine when on (a role, a weekday).
class TkPillChoice extends StatelessWidget {
  const TkPillChoice({super.key, required this.label, required this.on, required this.onTap, this.semantics, this.large = false});
  final String label;

  /// As drawn: small inside a card (a role), large on the sheet (Busy till).
  final bool large;
  final bool on;
  final VoidCallback onTap;
  final String? semantics;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = on ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.field, t.text);
    return TapArea(
      child: Semantics(
        button: true,
        selected: on,
        label: semantics ?? label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 160),
            constraints: BoxConstraints(minHeight: large ? 38 : 28),
            padding: EdgeInsets.symmetric(horizontal: large ? 14 : 10, vertical: large ? 9 : 5),
            decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
            child: Text(label, style: tkText(size: large ? 14.5 : 12.5, weight: 600, color: ink)),
          ),
        ),
      ),
    );
  }
}

/// A field that opens a picker: the web's soft field with what is chosen.
class TkPickBox extends StatelessWidget {
  const TkPickBox({super.key, required this.label, required this.shown, required this.onTap, this.chevron = false, this.icon});
  final String label;
  final String shown;
  final VoidCallback onTap;
  final bool chevron;

  /// An icon at its end (a calendar, a clock), as drawn; the box is taller.
  final String? icon;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: label,
      value: shown,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          constraints: BoxConstraints(minHeight: icon == null ? 36 : 48),
          padding: const EdgeInsets.only(left: 12, right: 11),
          decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(icon == null ? Tk.radiusSm : 14)),
          child: Row(
            children: [
              Expanded(child: Text(shown, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: icon == null ? 14 : 15, color: t.text))),
              if (chevron) TkIcon('chevron-down', size: 12, color: t.muted),
              if (icon != null) TkIcon(icon!, size: 18, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.text),
            ],
          ),
        ),
      ),
    );
  }
}

/// The web's `select`: the choice shown, a chevron; pressed, the choices to
/// pick one from, as a phone's browser lists them.
class TkSelect extends StatelessWidget {
  const TkSelect({super.key, required this.label, required this.value, required this.choices, required this.onChanged});
  final String label;
  final String value;
  final List<(String, String)> choices;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    final shown = choices.where((c) => c.$1 == value).map((c) => c.$2).firstOrNull ?? 'Choose…';
    return TkPickBox(
      label: label,
      shown: shown,
      chevron: true,
      onTap: () async {
        final v = await showTkModal<String>(
          context,
          title: label,
          builder: (context) {
            final t = TkTheme.tokens(context);
            return Material(
              type: MaterialType.transparency,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (final c in choices)
                    Semantics(
                      selected: c.$1 == value,
                      button: true,
                      child: TkChoiceRow(
                        control: TkRadio(checked: c.$1 == value),
                        label: Text(c.$2, style: tkText(size: 14, color: t.text)),
                        onTap: () => Navigator.of(context).pop(c.$1),
                      ),
                    ),
                ],
              ),
            );
          },
        );
        if (v != null) onChanged(v);
      },
    );
  }
}
