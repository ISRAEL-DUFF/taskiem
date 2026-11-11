// The person's own conversation with Taskiem (web/src/pages/TaskiemChat.tsx,
// lib/taskiemchat.ts, lib/taskiemSeen.ts): the one they have on WhatsApp,
// with the same words (status, orders, cash in, invoice …) and the same
// history from every channel. It scrolls, sends and looks as a business's
// conversation does; what differs is its content: Taskiem's answers carry
// reply buttons, a message may have come from WhatsApp, Telegram or Slack,
// a change made with a tool is a diary line, "how did we do?" is answered
// with a chart, and "typing…" is Taskiem preparing an answer.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'alerts.dart';
import 'attachments.dart';
import 'cards.dart';
import 'chats.dart';
import 'composer.dart';
import 'live.dart';
import 'thread.dart';
import 'words.dart';

/// TaskiemChat.tsx EVERY_MS: what is newer, every 5 seconds (20 with the
/// stream connected).
const _every = Duration(seconds: 5);

/// taskiemchat.ts NIL: a message that is not kept (a code) has no id.
const nilId = '00000000-0000-0000-0000-000000000000';

/// TaskiemChat.tsx CHANNEL.
const _channels = {'whatsapp': 'WhatsApp', 'telegram': 'Telegram', 'slack': 'Slack'};

/// A message of Taskiem's conversation; one of the person's on its way is
/// [local] (shown at once with a clock).
class TaskiemLine {
  TaskiemLine(this.m, {this.local = false});
  final TaskiemChatMessagesItem m;
  final bool local;
  String get id => m.id;
}

/// taskiemchat.ts mergeTaskiem: messages merged by id, oldest first; one
/// not kept (a code) stays as sent. A message of ours being sent gives way
/// to its stored copy when that comes first (by the stream); the copy of an
/// answer that carries its buttons' ids is kept over one that does not.
List<TaskiemLine> mergeTaskiem(List<TaskiemLine> a, List<TaskiemChatMessagesItem> b) {
  final arrived = b.where((m) => m.inbound && m.channel == 'web' && !a.any((x) => !x.local && x.id == m.id)).toList();
  bool sent(TaskiemLine x) {
    if (!x.local) return false;
    final at = arrived.indexWhere((y) => y.text == x.m.text);
    if (at < 0) return false;
    arrived.removeAt(at);
    return true;
  }

  final seen = <String, TaskiemLine>{};
  var n = 0;
  for (final x in [...a.where((x) => !sent(x)), ...b.map(TaskiemLine.new)]) {
    final key = x.id == nilId ? 'local-${n++}-${x.m.createdAt.toIso8601String()}' : x.id;
    final had = seen[key];
    final keep = (had?.m.buttons?.any((y) => y.id != null) ?? false) && !(x.m.buttons?.any((y) => y.id != null) ?? false);
    seen[key] = keep
        ? TaskiemLine(
            TaskiemChatMessagesItem(
              id: x.m.id,
              channel: x.m.channel,
              inbound: x.m.inbound,
              kind: x.m.kind,
              text: x.m.text,
              createdAt: x.m.createdAt,
              buttons: had!.m.buttons,
            ),
            local: x.local,
          )
        : x;
  }
  return seen.values.toList()..sort((p, q) => p.m.createdAt.compareTo(q.m.createdAt));
}

/// taskiemSeen.ts unreadAfter: where what Taskiem said since the person
/// last looked begins, and how much.
({String id, int count})? unreadAfter(List<TaskiemLine> list, DateTime? seen) {
  if (seen == null) return null;
  final later = list.where((x) => !x.m.inbound && x.m.createdAt.isAfter(seen)).toList();
  return later.isEmpty ? null : (id: later.first.id, count: later.length);
}

/// What Taskiem's conversation shows, loaded and followed as the web's is.
class TaskiemData extends ChangeNotifier {
  TaskiemData(this.chats, {this.follow = true});

  final ChatsData chats;
  final bool follow;

  TaskiemApi get api => chats.api;

  /// Null while loading; oldest first.
  List<TaskiemLine>? messages;
  ({String id, int count})? unread;
  bool busy = false;
  String? error;

  /// What a failed send gives back to the box.
  final _giveBack = StreamController<String>.broadcast();
  Stream<String> get givenBack => _giveBack.stream;

  Timer? _timer;
  StreamSubscription<LiveEvent>? _live;
  bool _disposed = false;
  int _localN = 0;

  /// The day's briefing, for those who sell or bill, with chat tools on.
  GetChatBriefingResponse? briefing;

  /// What needs the person, as cards after the conversation.
  List<ChatAlert> alerts = const [];

  /// TaskiemChat.tsx briefs: the briefing card is for those who sell or bill.
  bool get briefs => chats.chatTools && chats.auth.can('storefront.manage|invoice.manage');

  /// The briefing and the cards, read again (after one is dealt with).
  Future<void> loadCards() async {
    if (!chats.loaded) await chats.load();
    if (!chats.chatTools) return;
    try {
      alerts = (await api.getChatAlerts()).alerts;
      if (briefs) briefing = await api.getChatBriefing();
    } on Object {
      // The conversation stands without them.
    }
    _changed();
  }

  Future<void> open() async {
    unawaited(loadCards());
    try {
      final out = await api.taskiemHistory();
      final list = mergeTaskiem(const [], out.messages);
      unread = unreadAfter(list, chats.taskiemSeen);
      messages = list;
      _changed();
    } on ApiError catch (e) {
      error = e.message;
      _changed();
    } on Offline {
      error = 'You’re offline. The conversation shows when you’re back online.';
      _changed();
    }
    if (!follow) return;
    _live ??= chats.live?.events.listen((e) {
      if (e is LiveTaskiem) {
        _merge(e.messages.map(TaskiemChatMessagesItem.fromJson).toList());
      }
    });
    chats.live?.addListener(_reschedule);
    _reschedule();
  }

  bool? _wasConnected;
  void _reschedule() {
    final c = chats.live?.connected == true;
    if (_wasConnected == c) return;
    _wasConnected = c;
    _timer?.cancel();
    _timer = Timer.periodic(c ? _every * 4 : _every, (_) => pull().catchError((Object _) {}));
  }

  /// What is newer than the newest kept.
  Future<void> pull() async {
    final kept = messages?.where((x) => !x.local).toList() ?? const [];
    final after = kept.isEmpty ? null : kept.last.m.createdAt;
    final out = await api.taskiemHistory(after: after?.toUtc().toIso8601String());
    if (out.messages.isEmpty) return;
    _merge(out.messages);
  }

  void _merge(List<TaskiemChatMessagesItem> add) {
    if (_disposed) return;
    messages = mergeTaskiem(messages ?? const [], add);
    _changed();
  }

  /// The newest seen: what Taskiem said is read once the person is at the
  /// newest end (taskiemSeen.ts markSeen).
  void seen() {
    final kept = messages?.where((x) => !x.local).toList() ?? const [];
    if (kept.isEmpty) return;
    chats.markTaskiemSeen(kept.last.m.createdAt);
  }

  /// Said: shown at once with a clock, then what the server says. A
  /// button's choice is its id, with its title as the words.
  Future<void> say({String? text, String? reply}) async {
    final words = text?.trim() ?? '';
    TaskiemLine? mine;
    if (words.isNotEmpty) {
      mine = TaskiemLine(
        TaskiemChatMessagesItem(id: 'local-${++_localN}', channel: 'web', inbound: true, kind: 'text', text: words, createdAt: chats.now()),
        local: true,
      );
      messages = mergeTaskiem(messages ?? const [], const [])..add(mine);
    }
    busy = true;
    error = null;
    _changed();
    try {
      final out = await api.taskiemSay(
        body: TaskiemSayRequest(text: words.isEmpty ? null : words, reply: reply),
      );
      messages = mergeTaskiem((messages ?? const []).where((x) => x != mine).toList(), out.messages);
    } on ApiError catch (e) {
      error = e.message;
      _unsent(mine, reply);
    } on Offline {
      error = 'You’re offline. Send it again when you’re back online.';
      _unsent(mine, reply);
    } finally {
      busy = false;
      _changed();
    }
  }

  /// Not sent: what was typed comes back to the box to be sent again.
  void _unsent(TaskiemLine? mine, String? reply) {
    if (mine == null) return;
    messages = messages?.where((x) => x != mine).toList();
    if (reply == null) _giveBack.add(mine.m.text);
  }

  /// A voice note or a photo: read as one sent on WhatsApp.
  Future<void> sendFile(PickedFile f, String caption) async {
    busy = true;
    error = null;
    _changed();
    try {
      final j = await chats.auth.transport.upload(
        '/v1/taskiem-chat/files',
        bytes: f.bytes,
        filename: f.name,
        type: f.type,
        fields: {if (caption.isNotEmpty) 'text': caption},
      );
      _merge(TaskiemChat.fromJson(j as Map<String, dynamic>).messages);
    } on ApiError catch (e) {
      error = e.message;
    } on Offline {
      error = 'You’re offline. Send it again when you’re back online.';
    } finally {
      busy = false;
      _changed();
    }
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _live?.cancel();
    chats.live?.removeListener(_reschedule);
    _giveBack.close();
    super.dispose();
  }
}

/// Taskiem's conversation.
class TaskiemScreen extends StatefulWidget {
  const TaskiemScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers(), this.recorder});

  final TaskiemData data;
  final VoidCallback? onBack;
  final Pickers pickers;
  final Recorder? recorder;

  @override
  State<TaskiemScreen> createState() => _TaskiemScreenState();
}

class _TaskiemScreenState extends State<TaskiemScreen> {
  final _text = TextEditingController();
  StreamSubscription<String>? _back;

  @override
  void initState() {
    super.initState();
    if (widget.data.messages == null) widget.data.open();
    widget.data.chats.viewingTaskiem = true;
    _back = widget.data.givenBack.listen((words) {
      if (_text.text.isEmpty) _text.text = words;
    });
  }

  @override
  void dispose() {
    widget.data.chats.viewingTaskiem = false;
    _back?.cancel();
    _text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = widget.data;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: Semantics(
        label: 'Conversation with Taskiem',
        container: true,
        child: CustomPaint(
          painter: Wallpaper(t.chatWallpaper, t.chatDoodle),
          child: ListenableBuilder(
            listenable: d,
            builder: (context, _) => Column(
              children: [
                ColoredBox(
                  color: t.panel,
                  child: Padding(
                    padding: EdgeInsets.only(top: pad.top),
                    child: ChatHeadBar(
                      onBack: widget.onBack,
                      avatar: Container(
                        width: 42,
                        height: 42,
                        decoration: BoxDecoration(color: t.brand, shape: BoxShape.circle),
                        alignment: Alignment.center,
                        child: TkTaskiemMark(size: 20, color: t.brandDark),
                      ),
                      title: 'Taskiem',
                      subtitle: d.chats.auth.me?.tenantName ?? 'Your assistant',
                      typing: d.busy,
                    ),
                  ),
                ),
                Expanded(child: _thread(context, d)),
                Composer(
                  text: _text,
                  // TaskiemChat.tsx: with chat tools "Ask Taskiem, or @ to
                  // tag"; a phone shows the words before the comma.
                  hint: 'Ask Taskiem',
                  label: 'Message to Taskiem',
                  rules: taskiemFiles,
                  busy: d.busy,
                  bottom: pad.bottom,
                  pickers: widget.pickers,
                  recorder: widget.recorder,
                  onSend: (words, file) => file != null ? d.sendFile(file, words) : d.say(text: words),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _thread(BuildContext context, TaskiemData d) {
    final t = TkTheme.tokens(context);
    final list = d.messages;
    // For each of Taskiem's answers with buttons, the one pressed (the
    // person's next message, when it is the button's title); which of the
    // person's messages Taskiem has answered (their ticks go blue); and
    // whose buttons press (the newest answer with their ids, if nothing
    // has been said since).
    final pressed = <String, String>{};
    final answered = <String>{};
    String? pressable;
    final l = list ?? const <TaskiemLine>[];
    for (var i = 0; i < l.length; i++) {
      final m = l[i].m;
      final next = i + 1 < l.length ? l[i + 1].m : null;
      if (!m.inbound && (m.buttons?.isNotEmpty ?? false) && next != null && next.inbound) pressed[l[i].id] = next.text;
      if (m.inbound && l.skip(i + 1).any((x) => !x.m.inbound)) answered.add(l[i].id);
    }
    for (var i = l.length - 1; i >= 0; i--) {
      final m = l[i].m;
      if (m.inbound) break;
      if (m.buttons?.any((b) => b.id != null) ?? false) {
        pressable = l[i].id;
        break;
      }
    }
    return NotificationListener<ScrollEndNotification>(
      // Seen once the person is at the newest end.
      onNotification: (n) {
        if (n.metrics.maxScrollExtent - n.metrics.pixels <= 80) d.seen();
        return false;
      },
      child: ThreadView<TaskiemLine>(
        items: list,
        of: (id: (x) => x.id == nilId ? '${x.id}-${x.m.createdAt}' : x.id, at: (x) => x.m.createdAt, mine: (x) => x.m.inbound),
        now: d.chats.now(),
        unread: d.unread,
        placeAtMessagesEnd: true,
        tail: d.briefing == null && d.alerts.isEmpty
            ? null
            : Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (d.briefing != null) ...[
                    BriefingCard(briefing: d.briefing!, auth: d.chats.auth, onAsk: (w) => d.say(text: w)),
                    const SizedBox(height: 2),
                  ],
                  AlertCards(
                    alerts: d.alerts,
                    auth: d.chats.auth,
                    now: d.chats.now(),
                    onChanged: () {
                      d.loadCards();
                      d.pull();
                    },
                  ),
                ],
              ),
        errors: [?d.error],
        empty: Text.rich(
          TextSpan(
            children: [
              const TextSpan(text: 'This is the conversation you have with Taskiem on your phone. Say '),
              TextSpan(
                text: 'help',
                style: tkText(size: 13, weight: 700, color: t.muted),
              ),
              const TextSpan(text: ' to see what it can do, or '),
              TextSpan(
                text: 'status',
                style: tkText(size: 13, weight: 700, color: t.muted),
              ),
              const TextSpan(text: ' to see how the business is doing.'),
            ],
          ),
          textAlign: TextAlign.center,
          style: tkHint(t),
        ),
        render: (x, prev) {
          final m = x.m;
          if (m.kind == 'chart') {
            final chart = ChartData.parse(m.text);
            if (chart != null) return ChartCard(chart: chart);
          }
          if (m.kind == 'diary') return DiaryLine(words: m.text, when: 'You, ${clock(m.createdAt)}');
          // startsRun: a run is one side's.
          final first = prev == null || prev.m.inbound != m.inbound;
          return Padding(
            padding: EdgeInsets.only(top: first ? 6 : 0),
            child: _TaskiemBubble(
              x: x,
              first: first,
              read: answered.contains(x.id),
              choice: pressed[x.id],
              pressable: x.id == pressable && !d.busy,
              onPress: (b) => d.say(reply: b.id, text: b.title),
            ),
          );
        },
      ),
    );
  }
}

/// A message of Taskiem's conversation in its bubble: Taskiem's name on the
/// first of its run, its words (*bold* marked, a long answer folded), the
/// channel it came by, the time, the person's ticks, and reply buttons.
class _TaskiemBubble extends StatefulWidget {
  const _TaskiemBubble({required this.x, required this.first, required this.read, required this.choice, required this.pressable, required this.onPress});
  final TaskiemLine x;
  final bool first;
  final bool read;
  final String? choice;
  final bool pressable;
  final void Function(TaskiemChatMessagesItemButtonsItem b) onPress;

  @override
  State<_TaskiemBubble> createState() => _TaskiemBubbleState();
}

class _TaskiemBubbleState extends State<_TaskiemBubble> {
  int _taps = 0;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final m = widget.x.m;
    final mine = m.inbound;
    final buttons = m.buttons ?? const <TaskiemChatMessagesItemButtonsItem>[];
    // The time sits in the corner of the last line when there are no
    // buttons and it came from here.
    final inline = m.channel == 'web' && buttons.isEmpty;
    final meta = mine ? t.chatOutMeta : t.muted;
    final fold = reveal(m.text, _taps);
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (m.channel != 'web') ...[
          Semantics(
            label: 'Sent on ${_channels[m.channel] ?? m.channel}',
            excludeSemantics: true,
            child: Text(
              'on ${_channels[m.channel] ?? m.channel} ·',
              style: tkText(size: 11, lineHeight: 16 / 11, color: meta),
            ),
          ),
          const SizedBox(width: 4),
        ],
        Text(
          clock(m.createdAt),
          style: tkText(size: 11, lineHeight: 16 / 11, color: meta),
        ),
        if (mine) ...[
          const SizedBox(width: 4),
          widget.x.local
              ? Semantics(
                  label: 'Sending',
                  child: TkIcon('clock', size: 16, stroke: 2, color: meta),
                )
              : Semantics(
                  label: widget.read ? 'Read' : 'Delivered',
                  child: TkIcon('tick-double', size: 16, stroke: 2, color: widget.read ? t.chatTickRead : meta),
                ),
        ],
      ],
    );
    final words = Text.rich(
      TextSpan(
        children: [
          for (final p in boldParts(fold.shown))
            TextSpan(
              text: p.text,
              style: p.bold ? const TextStyle(fontWeight: FontWeight.w700, fontVariations: [FontVariation('wght', 700)]) : null,
            ),
          if (fold.folded) ...[
            const TextSpan(text: '… '),
            TextSpan(
              text: 'Read more',
              style: TextStyle(color: t.accent, fontWeight: FontWeight.w600, fontVariations: const [FontVariation('wght', 600)]),
              recognizer: null,
            ),
          ],
          // TaskiemText's `.stamp-gap` is never the wider `.mine` one: 52 px
          // either side, even beside the person's own ticks.
          if (inline) const WidgetSpan(alignment: PlaceholderAlignment.bottom, child: SizedBox(width: 52, height: 1)),
        ],
      ),
      style: tkText(size: 16, lineHeight: 23 / 16, color: t.text),
    );
    return BubbleFrame(
      mine: mine,
      first: widget.first,
      stamp: stamp,
      inline: inline,
      children: [
        if (!mine && widget.first) Text('Taskiem', style: tkText(size: 12, weight: 600, color: t.tintText)),
        if (fold.folded)
          Semantics(
            button: true,
            label: 'Read more',
            child: GestureDetector(onTap: () => setState(() => _taps++), child: words),
          )
        else
          words,
        if (buttons.isNotEmpty) _ReplyButtons(buttons: buttons, choice: widget.choice, pressable: widget.pressable, onPress: widget.onPress),
      ],
    );
  }
}

/// `.reply-buttons`: Taskiem's choices under its words, one a row, lines
/// between; pressable on its newest answer, else the one chosen ticked.
class _ReplyButtons extends StatelessWidget {
  const _ReplyButtons({required this.buttons, required this.choice, required this.pressable, required this.onPress});
  final List<TaskiemChatMessagesItemButtonsItem> buttons;
  final String? choice;
  final bool pressable;
  final void Function(TaskiemChatMessagesItemButtonsItem b) onPress;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: 'Replies',
      // `margin: 8px -6px -2px; border-top: 1px solid var(--border)`
      child: Bleed(
        top: 8,
        side: 6,
        bottom: 2,
        child: Container(
          decoration: BoxDecoration(
            border: Border(top: BorderSide(color: t.border)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var i = 0; i < buttons.length; i++)
                Builder(
                  builder: (context) {
                    final b = buttons[i];
                    final live = pressable && b.id != null;
                    final chosen = choice == b.title;
                    final row = Container(
                      constraints: const BoxConstraints(minHeight: 36),
                      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                      decoration: BoxDecoration(
                        border: i < buttons.length - 1 ? Border(bottom: BorderSide(color: t.border)) : null,
                      ),
                      alignment: Alignment.center,
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          if (chosen) ...[TkIcon('tick', size: 16, stroke: 2.2, color: t.text), const SizedBox(width: 6)],
                          Flexible(
                            child: Text(
                              b.title,
                              textAlign: TextAlign.center,
                              style: tkText(size: 14, weight: live || chosen ? 600 : 500, color: live ? t.accent : (chosen ? t.text : t.muted)),
                            ),
                          ),
                        ],
                      ),
                    );
                    return live
                        ? Semantics(
                            button: true,
                            label: b.title,
                            excludeSemantics: true,
                            child: TkPress(onTap: () => onPress(b), child: row),
                          )
                        : Semantics(label: chosen ? 'You chose ${b.title}' : b.title, excludeSemantics: true, child: row);
                  },
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A sales chart's numbers (ChatStaff.tsx SalesChart).
class ChartData {
  ChartData({required this.title, required this.totalKobo, required this.changePct, required this.than, required this.bars, required this.highlight});
  final String title;
  final int totalKobo;
  final num? changePct;
  final String than;
  final List<({String day, String label, int kobo})> bars;
  final int highlight;

  /// TaskiemChat.tsx parseChart: undefined when it cannot be read.
  static ChartData? parse(String text) {
    try {
      final j = jsonDecode(text) as Map<String, dynamic>;
      final bars = (j['bars'] as List).cast<Map<String, dynamic>>();
      return ChartData(
        title: j['title'] as String? ?? '',
        totalKobo: (j['total_kobo'] as num?)?.toInt() ?? 0,
        changePct: j['change_pct'] as num?,
        than: j['than'] as String? ?? '',
        bars: [for (final b in bars) (day: b['day'] as String? ?? '', label: b['label'] as String? ?? '', kobo: (b['kobo'] as num?)?.toInt() ?? 0)],
        highlight: (j['highlight'] as num?)?.toInt() ?? -1,
      );
    } on Object {
      return null;
    }
  }
}

/// ChatStaff.tsx ChartCard (`.tk-chart`): the title, the total, the change,
/// and a bar a day.
class ChartCard extends StatelessWidget {
  const ChartCard({super.key, required this.chart});
  final ChartData chart;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final heights = barHeights([for (final b in chart.bars) b.kobo]);
    final change = changeWords(chart.changePct, chart.than);
    final step = chart.bars.length > 10 ? 5 : 1;
    final down = (chart.changePct ?? 0) < 0;
    return Align(
      alignment: Alignment.centerLeft,
      child: Semantics(
        container: true,
        label: '${chart.title}: ${naira(chart.totalKobo)}',
        child: Container(
          constraints: const BoxConstraints(maxWidth: 420),
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: t.panel,
            borderRadius: BorderRadius.circular(22),
            border: dark ? Border.all(color: t.border) : null,
            boxShadow: dark
                ? null
                : [BoxShadow(color: cssMix(t.brand, const Color(0x00000000), 0.45), offset: const Offset(0, 10), blurRadius: 30, spreadRadius: -18)],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(chart.title.toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted)),
              const SizedBox(height: 4),
              Text(naira(chart.totalKobo), style: tkText(size: 28, weight: 800, letterSpacing: -0.56, color: t.text)),
              if (change != null) ...[const SizedBox(height: 4), Text(change, style: tkText(size: 14, weight: 700, color: down ? t.danger : t.ok))],
              const SizedBox(height: 14),
              Semantics(
                label: [for (final b in chart.bars) '${b.label} ${naira(b.kobo)}'].join(', '),
                excludeSemantics: true,
                child: SizedBox(
                  height: 120,
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (var i = 0; i < chart.bars.length; i++) ...[
                        if (i > 0) const SizedBox(width: 4),
                        Expanded(
                          child: Column(
                            mainAxisAlignment: MainAxisAlignment.end,
                            children: [
                              // `height: N%` of the 120 px column, the label's
                              // 14 px and the 4 px gap kept below it.
                              Container(
                                width: 28,
                                height: ((heights[i] * 100).round() / 100 * 120).clamp(0.0, 102.0),
                                decoration: BoxDecoration(
                                  color: i == chart.highlight || chart.highlight < 0
                                      ? (dark ? t.text : t.brand)
                                      : cssMix(dark ? t.text : t.brand, const Color(0x00000000), 0.25),
                                  borderRadius: const BorderRadius.vertical(top: Radius.circular(6), bottom: Radius.circular(3)),
                                ),
                              ),
                              const SizedBox(height: 4),
                              SizedBox(
                                height: 14,
                                child: Text(
                                  i % step == 0 || i == chart.bars.length - 1 ? chart.bars[i].label : '',
                                  maxLines: 1,
                                  style: tkText(size: 11, color: t.muted),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A CSS negative margin: [child] drawn [side] px wider on each side than
/// its place and [bottom] px into what is under it, [top] px below what is
/// above it. Laid out as its place's width less nothing: the bubble around
/// it keeps its size.
class Bleed extends SingleChildRenderObjectWidget {
  const Bleed({super.key, required super.child, this.top = 0, this.side = 0, this.bottom = 0});
  final double top;
  final double side;
  final double bottom;

  @override
  RenderObject createRenderObject(BuildContext context) => RenderBleed(top, side, bottom);

  @override
  void updateRenderObject(BuildContext context, RenderBleed renderObject) => renderObject
    ..top = top
    ..side = side
    ..bottom = bottom;
}

class RenderBleed extends RenderProxyBox {
  RenderBleed(this.top, this.side, this.bottom);
  double top;
  double side;
  double bottom;

  @override
  double computeMinIntrinsicWidth(double height) => (child?.getMinIntrinsicWidth(height) ?? 0) - 2 * side;

  @override
  double computeMaxIntrinsicWidth(double height) => (child?.getMaxIntrinsicWidth(height) ?? 0) - 2 * side;

  @override
  double computeMinIntrinsicHeight(double width) => top + (child?.getMinIntrinsicHeight(width + 2 * side) ?? 0) - bottom;

  @override
  double computeMaxIntrinsicHeight(double width) => top + (child?.getMaxIntrinsicHeight(width + 2 * side) ?? 0) - bottom;

  @override
  void performLayout() {
    final c = child!;
    final w = constraints.maxWidth + 2 * side;
    c.layout(BoxConstraints(minWidth: w, maxWidth: w), parentUsesSize: true);
    size = constraints.constrain(Size(constraints.maxWidth, top + c.size.height - bottom));
  }

  @override
  void paint(PaintingContext context, Offset offset) => context.paintChild(child!, offset + Offset(-side, top));

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) => result.addWithPaintOffset(
    offset: Offset(-side, top),
    position: position,
    hitTest: (r, p) => child!.hitTest(r, position: p),
  );

  @override
  void applyPaintTransform(RenderBox child, Matrix4 transform) => transform.translateByDouble(-side, top, 0, 1);
}
