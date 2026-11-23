// Menus in the Taskiem chat (docs/spec/chat-menus.md §6; the web's
// pages/ChatMenu.tsx): a message of kind "menu" drawn as a card from the
// person's own catalogue (GET /v1/chat/actions). A topic's menu shows its
// first five actions as rows, the rest behind "All N" in a sheet; the
// topics are tiles. A row shows its answer or asks step by step (sent back
// as "act:<id>"), or opens its tool or its page here. Once a row is
// pressed, its ✕ closes it, the person says something else, or a newer menu
// comes, the card folds to one line saying what happened (21 November
// 2026: no "Show again", the menu has done its work).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError, Offline;
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import 'approve.dart' show TkTextBox;
import 'pay_sheet.dart' show PaySheet;
import '../kit/icon.dart';
import '../theme/look.dart';
import '../theme/theme.dart';

/// At most this many of a topic's rows show in the chat (§6.1).
const menuRows = 5;

/// A menu message: a topic's (null for the topics), what Taskiem
/// understood, the row it picked with what it took from the message, and
/// that row's command as a tip.
class MenuMessage {
  const MenuMessage({this.topic, this.line, this.pick, this.values = const {}, this.tip, this.done, this.chose});
  final String? topic;
  final String? line;
  final String? pick;
  final Map<String, String> values;
  final String? tip;

  /// How it ended, as the server kept it: chose, closed or passed.
  final String? done;

  /// The action chosen, when it ended so.
  final String? chose;

  static MenuMessage? parse(String text) {
    try {
      final j = jsonDecode(text);
      if (j is! Map<String, dynamic>) return null;
      return MenuMessage(
        topic: j['topic'] as String?,
        line: j['line'] as String?,
        pick: j['pick'] as String?,
        values: {for (final e in ((j['values'] as Map?) ?? const {}).entries) '${e.key}': '${e.value}'},
        tip: j['tip'] as String?,
        done: j['done'] as String?,
        chose: j['chose'] as String?,
      );
    } on FormatException {
      return null;
    }
  }
}

// The catalogue's icons, as the app draws them.
const _icons = {
  'sell': 'b-cart', 'stock': 'b-box', 'products': 'b-bag', 'services': 'b-clock', 'prices': 'b-fees', 'orders': 'b-receipt',
  'tables': 'b-table', 'purchases': 'b-truck', 'bills': 'b-receipt', 'invoices': 'b-receipt', 'money': 'b-money', 'rider': 'b-bike',
  'storefront': 'b-store', 'bookings': 'b-calendar', 'customers': 'b-hand', 'tasks': 'b-check', 'approvals': 'b-check-ring',
  'workflows': 'b-flow', 'saved': 'b-pin', 'school': 'b-school', 'you': 'b-person', 'handover': 'b-cash',
};
String menuIcon(String name) => _icons[name] ?? 'b-more';

/// A topic's colour, from the look.
Color topicColor(TkLook look, String color) => switch (color) {
  'lime' => look.lime,
  'teal' => look.teal,
  'orange' => look.orange,
  'violet' => look.violet,
  'blue' => look.blue,
  'red' => look.red,
  _ => look.teal,
};

final _catalogues = Expando<({DateTime at, Future<List<ChatTopic>> topics})>();
final _read = Expando<List<ChatTopic>>();

/// How long a read catalogue serves new menus: a part of the business
/// turned off, a role changed or the most used reordered shows within it.
const _fresh = Duration(minutes: 1);

/// The person's catalogue, read again once a minute old.
Future<List<ChatTopic>> catalogueOf(Auth auth) {
  final had = _catalogues[auth];
  if (had != null && DateTime.now().difference(had.at) < _fresh) return had.topics;
  final f = auth.api.chatActions().then((r) => _read[auth] = r.topics);
  _catalogues[auth] = (at: DateTime.now(), topics: f);
  f.catchError((Object _) {
    _catalogues[auth] = null;
    return <ChatTopic>[];
  });
  return f;
}

/// A topic's actions, the picked one first.
List<ChatAction> orderedActions(ChatTopic t, String? pick) {
  final i = t.actions.indexWhere((a) => a.id == pick);
  if (i <= 0) return t.actions;
  return [t.actions[i], ...t.actions.take(i), ...t.actions.skip(i + 1)];
}

class MenuCard extends StatefulWidget {
  const MenuCard({
    super.key,
    required this.auth,
    this.id,
    required this.menu,
    required this.open,
    required this.onAct,
    required this.onTool,
    required this.onPage,
    required this.onForm,
    this.busy = false,
    this.head = true,
    this.tiles,
    this.onTopic,
  });

  /// At most this many topics' tiles, then "All topics" for the rest
  /// (Today's panel, menus-everywhere.md §5); every one when null.
  final int? tiles;

  /// A tile pressed: true when the screen shows the topic itself (in a
  /// sheet, from Today); else the card turns into it.
  final bool Function(String topic)? onTopic;

  /// The topics' own heading ("What would you like to do?"): not in the
  /// tools panel, whose chip says it.
  final bool head;
  final Auth auth;

  /// The menu message's id, to close it; null for the menu in the tools
  /// panel (menus-everywhere.md §4), which is no message and has no ✕.
  final String? id;
  final MenuMessage menu;

  /// The newest menu, nothing said since: open; any other is folded.
  final bool open;
  final bool busy;

  /// A row sent back to Taskiem, as a button's press.
  final void Function(ChatAction a) onAct;

  /// A form's tool opened here; false when the app has no such tool.
  final bool Function(String tool, bool add) onTool;

  /// A page opened here; false when the app has no such screen.
  final bool Function(String path) onPage;

  /// An action's form sent to Taskiem (M4): the reply and its words.
  final void Function(String reply, String text) onForm;

  @override
  State<MenuCard> createState() => _MenuCardState();
}

class _MenuCardState extends State<MenuCard> {
  // Drawn whole at once when the catalogue was read before (it is read as
  // the conversation opens), so the chat does not jump as it comes.
  late List<ChatTopic>? _topics = _read[widget.auth];
  String? _topic;

  /// How it ended here (chose, closed, passed) and the row chosen.
  late String? _ended = widget.menu.done;
  String? _chosen;
  late bool _shown = widget.open && widget.menu.done == null;

  /// "All topics" pressed under a limit of tiles.
  bool _allTiles = false;

  @override
  void initState() {
    super.initState();
    _topic = widget.menu.topic;
    if (_topics != null) return;
    _load();
  }

  bool _failed = false;

  void _load() {
    unawaited(catalogueOf(widget.auth).then((t) {
      if (mounted) setState(() => _topics = t);
    }, onError: (Object _) {
      if (mounted) setState(() => _failed = true);
    }));
  }

  @override
  void didUpdateWidget(MenuCard old) {
    super.didUpdateWidget(old);
    if (old.open && !widget.open) {
      _shown = false;
      _ended ??= 'passed';
    }
  }

  void _fold(ChatAction a, {bool tell = false}) {
    setState(() {
      _ended = 'chose';
      _chosen = a.title;
      _shown = false;
    });
    // Nothing goes to Taskiem for a tool or a page: the server is told, so
    // the menu stays folded to what was chosen.
    final id = widget.id;
    if (tell && id != null) unawaited(widget.auth.api.closeChatMenu(id: id, body: CloseChatMenuRequest(chose: a.id)).then((_) {}, onError: (Object _) {}));
  }

  void _close() {
    setState(() {
      _ended = 'closed';
      _shown = false;
    });
    final id = widget.id;
    if (id != null) unawaited(widget.auth.api.closeChatMenu(id: id, body: const CloseChatMenuRequest()).then((_) {}, onError: (Object _) {}));
  }

  Future<void> _press(ChatAction a) async {
    final fields = a.fields ?? const [];
    final picked = a.id == widget.menu.pick && _topic == widget.menu.topic;
    // The row Taskiem picked with values it took from the message: the form
    // with them in it, even for an action with a tool of its own, whose
    // form would start empty.
    final carry = picked && widget.menu.values.isNotEmpty && fields.isNotEmpty;
    // A payment (confirm: passkey): the Pay sheet prepares it and pays it
    // with a passkey; nothing is sent to Taskiem as words.
    if (a.confirm == 'passkey' && fields.isNotEmpty) {
      final paid = await showTkSheet<Payment>(
        context,
        (_) => PaySheet(auth: widget.auth, action: a, initial: picked ? widget.menu.values : const {}),
      );
      if (paid != null && mounted) _fold(a);
      return;
    }
    if (!carry && a.opens == 'form' && a.tool != null && widget.onTool(a.tool!, a.add == true)) return _fold(a, tell: true);
    if (a.opens == 'page' && a.page != null && widget.onPage(a.page!)) return _fold(a, tell: true);
    // An action that takes values: its form here (M4), filled with what
    // Taskiem took from the message.
    if (fields.isNotEmpty) {
      final values = await showTkSheet<Map<String, String>>(
        context,
        (_) => ActionFormSheet(auth: widget.auth, action: a, initial: picked ? widget.menu.values : const {}),
      );
      if (values == null || !mounted) return;
      _fold(a);
      final shown = [for (final f in fields) values[f.key] ?? ''].where((v) => v.isNotEmpty).toList();
      widget.onForm('form:${a.id}:${jsonEncode(values)}', shown.isEmpty ? a.title : '${a.title}: ${shown.join(', ')}');
      return;
    }
    _fold(a);
    widget.onAct(a);
  }

  Future<void> _all(ChatTopic t, List<ChatAction> acts) => showTkSheet<void>(
    context,
    (sheet) => TkSheet(
      title: t.name,
      count: acts.length,
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [for (final a in acts) _row(t, a, close: () => Navigator.of(sheet).pop())],
      ),
    ),
  );

  Widget _row(ChatTopic t, ChatAction a, {VoidCallback? close}) {
    final look = TkLook.of(context);
    final picked = a.id == widget.menu.pick && _topic == widget.menu.topic;
    final name = picked ? widget.menu.values['name'] : null;
    return TkListRow(
      title: name == null ? a.title : '${a.title}: $name',
      sub: a.line,
      subLines: 1,
      titleLines: 1,
      icon: menuIcon(a.icon),
      square: topicColor(look, t.color),
      semanticsLabel: '${a.title}. ${a.line}.',
      onTap: widget.busy && a.opens != 'form' && a.opens != 'page'
          ? null
          : () {
              close?.call();
              unawaited(_press(a));
            },
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final topics = _topics;
    final box = BoxDecoration(
      color: t.panel,
      borderRadius: BorderRadius.circular(20),
      border: dark ? Border.all(color: t.border) : null,
      boxShadow: dark ? null : [BoxShadow(color: cssMix(t.brand, const Color(0x00000000), 0.35), offset: const Offset(0, 8), blurRadius: 24, spreadRadius: -16)],
    );
    Widget card(Widget child) => Align(
      alignment: Alignment.centerLeft,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 400),
        margin: const EdgeInsets.symmetric(vertical: 4),
        decoration: box,
        clipBehavior: Clip.antiAlias,
        child: child,
      ),
    );
    if (topics == null && _failed) {
      return card(
        Padding(
          padding: const EdgeInsets.fromLTRB(14, 4, 4, 4),
          child: Row(
            children: [
              Expanded(child: Text('The menu could not be loaded.', style: tkText(size: 13.5, color: t.muted))),
              TkPress(
                onTap: () => setState(() {
                  _failed = false;
                  _load();
                }),
                child: Container(
                  constraints: const BoxConstraints(minHeight: 48, minWidth: 48),
                  alignment: Alignment.center,
                  padding: const EdgeInsets.symmetric(horizontal: 10),
                  child: Text('Try again', style: tkText(size: 13, weight: 600, color: dark ? t.brandDark : t.brand)),
                ),
              ),
            ],
          ),
        ),
      );
    }
    if (topics == null) return card(const SizedBox(height: 96));
    // The action chosen (here, or kept by the server), and its topic when
    // the card was the topics.
    final chosenAction = widget.menu.chose == null ? null : topics.expand((x) => x.actions).where((a) => a.id == widget.menu.chose).firstOrNull;
    final topicId = _topic ?? (chosenAction == null ? null : topics.where((x) => x.actions.contains(chosenAction)).firstOrNull?.id);
    final topic = topicId == null ? null : topics.where((x) => x.id == topicId).firstOrNull;
    Widget square(String icon, Color color, {double size = 36}) => Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(size * 0.3)),
      alignment: Alignment.center,
      child: TkIcon(icon, size: size * 0.48, stroke: 2.2, color: const Color(0xFFFFFFFF)),
    );
    Widget head(String title, String? line, Widget lead) => Padding(
      padding: const EdgeInsets.fromLTRB(14, 12, 4, 8),
      child: Row(
        children: [
          lead,
          const SizedBox(width: 11),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: tkText(size: 16, weight: 700, color: t.text)),
                if (line != null && line.isNotEmpty) Text(line, style: tkText(size: 13, color: t.muted, lineHeight: 1.35)),
              ],
            ),
          ),
          // Its ✕: folds to "Closed, nothing done" (21 November 2026).
          if (widget.id != null) Semantics(
            button: true,
            label: 'Close this menu',
            excludeSemantics: true,
            child: TkPress(
              onTap: _close,
              child: Container(
                key: const ValueKey('menu-close'),
                width: 48,
                height: 48,
                alignment: Alignment.center,
                child: TkIcon('b-x', size: 18, color: t.muted),
              ),
            ),
          ),
        ],
      ),
    );
    Widget footButton(String label, VoidCallback onTap) => Expanded(
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 48),
          alignment: Alignment.center,
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Text(label, textAlign: TextAlign.center, style: tkText(size: 13.5, weight: 600, color: dark ? t.brandDark : t.brand)),
        ),
      ),
    );

    if (!_shown) {
      // What happened, said once: no way back into a menu that is done.
      final what = switch (_ended) {
        'chose' => 'You chose ${_chosen ?? chosenAction?.title ?? 'an action'}',
        'closed' => 'Closed, nothing done',
        _ => 'Not used',
      };
      return Semantics(
        container: true,
        child: card(
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 14, 10),
            child: Row(
              children: [
                square(topic == null ? 'b-tools' : menuIcon(topic.icon), topic == null ? look.lime : topicColor(look, topic.color), size: 28),
                const SizedBox(width: 10),
                Flexible(
                  child: Text.rich(
                    TextSpan(
                      children: [
                        TextSpan(text: topic?.name ?? 'Topics', style: tkText(size: 13.5, weight: 700, color: t.text)),
                        TextSpan(text: ' · $what', style: tkText(size: 13.5, color: t.muted)),
                      ],
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          ),
        ),
      );
    }

    if (topic == null) {
      return card(
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (widget.head) head('What would you like to do?', widget.menu.line, square('b-tools', look.lime)) else const SizedBox(height: 10),
            Padding(
              padding: const EdgeInsets.fromLTRB(6, 0, 6, 8),
              child: LayoutBuilder(
                builder: (context, c) {
                  final w = c.maxWidth / 4;
                  return Wrap(
                    children: [
                      for (final x in _allTiles || widget.tiles == null ? topics : topics.take(widget.tiles!))
                        SizedBox(
                          width: w,
                          child: TkPress(
                            onTap: () {
                              if (widget.onTopic?.call(x.id) ?? false) return;
                              setState(() => _topic = x.id);
                            },
                            child: Padding(
                              padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 2),
                              child: Column(
                                children: [
                                  square(menuIcon(x.icon), topicColor(look, x.color), size: 38),
                                  const SizedBox(height: 6),
                                  Text(
                                    x.name,
                                    textAlign: TextAlign.center,
                                    maxLines: 2,
                                    style: tkText(size: 11.5, weight: 500, color: t.text, lineHeight: 1.2),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        ),
                      // The rest behind one more tile.
                      if (!_allTiles && widget.tiles != null && topics.length > widget.tiles!)
                        SizedBox(
                          width: w,
                          child: TkPress(
                            onTap: () => setState(() => _allTiles = true),
                            child: Padding(
                              padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 2),
                              child: Column(
                                children: [
                                  square('b-tools', look.lime, size: 38),
                                  const SizedBox(height: 6),
                                  Text('All topics', textAlign: TextAlign.center, maxLines: 2, style: tkText(size: 11.5, weight: 500, color: t.text, lineHeight: 1.2)),
                                ],
                              ),
                            ),
                          ),
                        ),
                    ],
                  );
                },
              ),
            ),
          ],
        ),
      );
    }

    final acts = orderedActions(topic, _topic == widget.menu.topic ? widget.menu.pick : null);
    final tip = widget.menu.tip;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        card(
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              head(
                topic.name,
                (_topic == widget.menu.topic ? widget.menu.line : null) ?? 'What would you like to do?',
                square(menuIcon(topic.icon), topicColor(look, topic.color)),
              ),
              for (final a in acts.take(menuRows)) _row(topic, a),
              Container(
                decoration: BoxDecoration(border: Border(top: BorderSide(color: t.border))),
                child: Row(
                  children: [
                    if (acts.length > menuRows) footButton('All ${acts.length} for ${topic.name}', () => unawaited(_all(topic, acts))),
                    footButton('All topics', () => setState(() => _topic = null)),
                  ],
                ),
              ),
            ],
          ),
        ),
        if (tip != null && tip.isNotEmpty && _topic == widget.menu.topic)
          Padding(
            padding: const EdgeInsets.fromLTRB(6, 0, 6, 4),
            child: Text.rich(
              TextSpan(
                children: [
                  const TextSpan(text: 'Next time you can type: '),
                  TextSpan(text: tip, style: tkText(size: 12.5, weight: 600, color: t.text)),
                ],
              ),
              style: tkText(size: 12.5, color: t.muted),
            ),
          ),
      ],
    );
  }
}

/// An action's form (chat-menus.md M4; the web's ChatMenu.tsx ActionForm):
/// a field for each of its values, the business's own choices as chips
/// above it; its button gives the values back to be sent to Taskiem, read
/// as the action's command typed, the button being the person's yes. An
/// action with `confirm: "passkey"` (a payment) is not this form but the
/// Pay sheet (pay_sheet.dart), which prepares the payment itself.
class ActionFormSheet extends StatefulWidget {
  const ActionFormSheet({super.key, required this.auth, required this.action, this.initial = const {}});
  final Auth auth;
  final ChatAction action;
  final Map<String, String> initial;

  @override
  State<ActionFormSheet> createState() => _ActionFormSheetState();
}

class _ActionFormSheetState extends State<ActionFormSheet> {
  late final List<ChatField> _fields = widget.action.fields ?? const [];
  late final Map<String, TextEditingController> _boxes = {
    for (final f in _fields) f.key: TextEditingController(text: widget.initial[f.key] ?? ''),
  };
  Map<String, List<String>> _choices = const {};

  static const _typed = {'text', 'phone', 'money', 'number', 'signed', 'choice'};

  @override
  void initState() {
    super.initState();
    for (final b in _boxes.values) {
      b.addListener(() => setState(() {}));
    }
    if (_fields.any((f) => !_typed.contains(f.kind))) {
      unawaited(widget.auth.api.chatActionChoices(id: widget.action.id).then((r) {
        if (!mounted) return;
        setState(() => _choices = {for (final e in r.fields.entries) e.key: [for (final x in (e.value as List)) '$x']});
      }, onError: (Object _) {}));
    }
  }

  @override
  void dispose() {
    for (final b in _boxes.values) {
      b.dispose();
    }
    super.dispose();
  }

  Widget _chips(ChatField f) => TkChips<String>(
    label: f.label,
    padding: EdgeInsets.zero,
    chips: [for (final c in f.choices ?? _choices[f.key] ?? const <String>[]) (c, c)],
    on: _boxes[f.key]!.text,
    onPick: (v) => _boxes[f.key]!.text = v,
  );

  bool get _ready => _fields.every((f) => f.optional == true || _boxes[f.key]!.text.trim().isNotEmpty);

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: widget.action.title,
    action: widget.action.title,
    onAction: _ready ? () => Navigator.of(context).pop({for (final f in _fields) f.key: _boxes[f.key]!.text.trim()}) : null,
    second: 'Cancel',
    onSecond: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(
        children: [
          for (final f in _fields) ...[
            if (f.kind == 'choice')
              TkKitField(label: f.label, hint: f.hint, field: false, child: _chips(f))
            else
              TkKitField(
                label: f.optional == true ? '${f.label} (optional)' : f.label,
                hint: f.hint,
                child: TkTextBox(
                  controller: _boxes[f.key]!,
                  size: 15,
                  height: 46,
                  max: 150,
                  money: f.kind == 'money',
                  signed: f.kind == 'signed',
                  digits: f.kind == 'number' ? 6 : null,
                ),
              ),
            // The business's own: its riders, their areas, its items.
            if (f.kind != 'choice' && (_choices[f.key] ?? const []).isNotEmpty) _chips(f),
          ],
        ],
      ),
    ],
  );
}


/// What Taskiem said to a row pressed outside the Taskiem chat
/// (menus-everywhere.md §3), in a sheet: its words, a question's buttons
/// (answered here, the answer said in turn), and the way to the Taskiem
/// chat, where all of it is kept.
Future<void> showActionAnswer(BuildContext context, Auth auth, String title, List<TaskiemMessage> said, {VoidCallback? onOpenChat}) =>
    showTkSheet<void>(context, (sheet) => _ActionAnswer(auth: auth, title: title, said: said, onOpenChat: onOpenChat == null ? null : () {
      Navigator.of(sheet).pop();
      onOpenChat();
    }));

class _ActionAnswer extends StatefulWidget {
  const _ActionAnswer({required this.auth, required this.title, required this.said, this.onOpenChat});
  final Auth auth;
  final String title;
  final List<TaskiemMessage> said;
  final VoidCallback? onOpenChat;

  @override
  State<_ActionAnswer> createState() => _ActionAnswerState();
}

class _ActionAnswerState extends State<_ActionAnswer> {
  late List<TaskiemMessage> _said = widget.said;
  bool _busy = false;

  /// A button of Taskiem's question pressed (a Yes): sent to the Taskiem
  /// chat as its press is there, the answer shown in its place.
  Future<void> _press(TaskiemMessageButtonsItem b) async {
    if (_busy || b.id == null) return;
    setState(() => _busy = true);
    try {
      final r = await widget.auth.api.taskiemSay(body: TaskiemSayRequest(text: b.title, reply: b.id));
      if (mounted) setState(() => _said = r.messages);
    } on Object {
      if (mounted) showTkToast(context, 'That did not go. Try it in your Taskiem chat.', error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final words = <String>[];
    final buttons = <TaskiemMessageButtonsItem>[];
    for (final m in _said) {
      if (m.inbound) continue;
      if (m.kind == 'menu') {
        final line = MenuMessage.parse(m.text)?.line;
        if (line != null && line.isNotEmpty) words.add(line);
      } else if (m.text.trim().isNotEmpty && m.kind != 'chart') {
        words.add(m.text.replaceAll('*', '').trim());
      }
      buttons.addAll((m.buttons ?? const []).where((b) => b.id != null));
    }
    return TkFormSheet(
      title: widget.title,
      action: 'Done',
      onAction: () => Navigator.of(context).pop(),
      second: widget.onOpenChat == null ? null : 'Taskiem chat',
      onSecond: widget.onOpenChat,
      children: [
        TkFormGroup(
          children: [
            Text(
              words.isEmpty ? 'Done. It is in your Taskiem chat.' : words.join('\n\n'),
              key: const ValueKey('action-answer'),
              style: tkText(size: 15, lineHeight: 1.45, color: t.text),
            ),
            if (buttons.isNotEmpty)
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final (i, b) in buttons.indexed) TkSmallButton(label: b.title, primary: i == 0, onPressed: _busy ? null : () => unawaited(_press(b))),
                ],
              ),
          ],
        ),
      ],
    );
  }
}


/// A row pressed outside the Taskiem chat (menus-everywhere.md §3): done
/// through the Taskiem chat (POST /v1/chat/actions/{id}/run), its answer in
/// a sheet; in a business's conversation ([link]) what Taskiem said is left
/// there as a note, given to [onNote].
Future<void> runActionHere(
  BuildContext context,
  Auth auth,
  String id,
  String title,
  Map<String, String>? values, {
  String? link,
  void Function(LinkMessage)? onNote,
  VoidCallback? onOpenChat,
}) async {
  try {
    final r = await auth.api.runChatAction(id: id, body: RunChatActionRequest(link: link, values: values));
    final note = r.note;
    if (note != null) onNote?.call(note);
    if (!context.mounted) return;
    await showActionAnswer(context, auth, title, r.messages, onOpenChat: onOpenChat);
  } on ApiError catch (e) {
    if (context.mounted) showTkToast(context, e.message.isEmpty ? 'That could not be done.' : e.message, error: true);
  } on Offline {
    if (context.mounted) showTkToast(context, 'You are offline. Try again when you are back.', error: true);
  }
}

/// A form row's reply (`form:<id>:<values>`) as its id and values.
(String, Map<String, String>) formOf(String reply) {
  final rest = reply.substring('form:'.length);
  final i = rest.indexOf(':');
  return (rest.substring(0, i), (jsonDecode(rest.substring(i + 1)) as Map).map((k, v) => MapEntry('$k', '$v')));
}
