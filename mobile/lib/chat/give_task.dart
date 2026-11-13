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
import '../kit/modal.dart';
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

/// GiveThis's press: the people, then the form; a word when it is given.
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
  final given = await showTkModal<bool>(
    context,
    title: 'Give this to someone',
    builder: (_) => GiveTask(auth: auth, people: people, link: link),
  );
  if (given == true && context.mounted) showTkToast(context, 'Task given. They hear in their chat.');
}

/// Tasks.tsx GiveTask: the form; it pops true once the task is given.
class GiveTask extends StatefulWidget {
  const GiveTask({super.key, required this.auth, required this.people, this.link, this.now});
  final Auth auth;
  final List<ListStaffTasksResponsePeopleItem> people;
  final TaskLink? link;
  final DateTime Function()? now;

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

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = taskProblem(_f);
    final f = _f;
    Widget field(String label, Widget child, {String? hint}) => Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(padding: const EdgeInsets.only(bottom: 6), child: Text(label, style: tkText(size: 13, weight: 500, color: t.text))),
          child,
          if (hint != null) Padding(padding: const EdgeInsets.only(top: 6), child: Text(hint, style: tkHint(t))),
        ],
      ),
    );
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: TkField(label: 'What is to be done', controller: _what, onChanged: (v) => setState(() => f.what = v)),
        ),
        field(
          'Who it is for',
          TkSelect(
            label: 'Who it is for',
            value: f.who,
            choices: whoChoices(widget.people),
            onChanged: (v) => setState(() => f.who = v),
          ),
          hint: 'For anyone in a role, the first to take it has it.',
        ),
        field(
          'When',
          TkSelect(
            label: 'When',
            value: f.when,
            choices: const [('none', 'Any time'), ('by', 'By a time'), ('day', 'Every day'), ('week', 'Every week')],
            onChanged: (v) => setState(() => f.when = v),
          ),
        ),
        if (f.when == 'by') field('Day', TkPickBox(label: 'Day', shown: f.date, onTap: _pickDay)),
        if (f.when == 'week')
          field(
            'On',
            TkSelect(label: 'On', value: f.day, choices: weekdays, onChanged: (v) => setState(() => f.day = v)),
          ),
        if (f.when != 'none') field(f.when == 'by' ? 'By' : 'Due at', TkPickBox(label: f.when == 'by' ? 'By' : 'Due at', shown: f.time.isEmpty ? '--:--' : f.time, onTap: _pickTime)),
        Semantics(
          checked: f.photo,
          child: TkChoiceRow(
            radio: false,
            control: TkCheckbox(checked: f.photo),
            label: Text('A photo when it is done', style: tkText(size: 14, color: t.text)),
            onTap: () => setState(() => f.photo = !f.photo),
          ),
        ),
        if (_tried && problem != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Semantics(liveRegion: true, child: Text(problem, style: tkText(size: 14, color: t.danger))),
          ),
        if (_error != null) TkErrorBox(_error!),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkPlainButton(label: 'Give the task', primary: true, onTap: _busy ? null : _give),
            TkPlainButton(label: 'Cancel', onTap: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

/// A field that opens a picker: the web's soft field with what is chosen.
class TkPickBox extends StatelessWidget {
  const TkPickBox({super.key, required this.label, required this.shown, required this.onTap, this.chevron = false});
  final String label;
  final String shown;
  final VoidCallback onTap;
  final bool chevron;

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
          constraints: const BoxConstraints(minHeight: 36),
          padding: const EdgeInsets.only(left: 12, right: 11),
          decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(Tk.radiusSm)),
          child: Row(
            children: [
              Expanded(child: Text(shown, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14, color: t.text))),
              if (chevron) TkIcon('chevron-down', size: 12, color: t.muted),
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
