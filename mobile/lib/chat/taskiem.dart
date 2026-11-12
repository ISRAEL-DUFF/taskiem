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

import 'package:flutter/gestures.dart' show TapGestureRecognizer;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'alerts.dart';
import 'attachments.dart';
import 'cards.dart';
import 'chats.dart';
import 'composer.dart';
import 'connect.dart';
import 'live.dart';
import 'explore.dart' show showExploreCard;
import 'menu.dart';
import 'ref_card.dart';
import 'tag_box.dart';
import 'tags.dart';
import 'thread.dart';
import 'tool_forms.dart' show toolDetail;
import 'sell_sheet.dart' show showHandoverSheet, showSellSheet;
import 'availability_sheet.dart' show showAvailabilitySheet;
import 'bookings_page.dart' show BookingsPage;
import 'storefront_orders_page.dart' show DeliveriesPage, openStorefrontOrder, showRiderSheet;
import 'tool_sheet.dart';
import 'tools.dart';
import 'tools_panel.dart';
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
/// TaskiemChat.tsx PAGE: the server's page of history.
const _page = 50;

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
    // The history keeps no one-time link (a connect link, a draft's
    // address are cut or left out): the words as they came live keep it.
    final words = had != null && !had.local && keptWithoutLinks(had.m.text, x.m.text);
    seen[key] = keep || words
        ? TaskiemLine(
            TaskiemChatMessagesItem(
              id: x.m.id,
              channel: x.m.channel,
              inbound: x.m.inbound,
              kind: x.m.kind,
              text: words ? had.m.text : x.m.text,
              createdAt: x.m.createdAt,
              buttons: keep ? had!.m.buttons : x.m.buttons,
            ),
            local: x.local,
          )
        : x;
  }
  return seen.values.toList()..sort((p, q) => p.m.createdAt.compareTo(q.m.createdAt));
}

/// Whether [kept] is [live] as the history keeps it: its links cut
/// (chat_history.go historyText) or a link button's line left out, and
/// [live] had a link to lose.
bool keptWithoutLinks(String live, String kept) {
  if (live == kept || !live.contains(RegExp(r'https?://'))) return false;
  String bare(String s) => s.replaceAll(RegExp(r'https?://[^\s<>"]+'), '').replaceAll(RegExp(r'\n[^\n]*: *$'), '').trim();
  return bare(live) == bare(kept);
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

  /// TaskiemChat.tsx: what "/invoice" and the like may tag (all the person
  /// may see), the business's own stock (its products on "#"), and what
  /// suppliers sell ("#" after them).
  List<ChatRef> refs = const [];
  List<StockItem> ownStock = const [];
  List<SupplierPrices> suppliers = const [];

  Future<void> loadTags() async {
    if (!chats.loaded) await chats.load();
    final tools = chats.chatTools;
    await Future.wait([
      () async {
        try {
          suppliers = (await api.listSupplierPrices()).suppliers;
        } on Object {
          // None offered.
        }
      }(),
      if (tools && chats.auth.can('table.read'))
        () async {
          try {
            ownStock = (await api.getStock()).items;
          } on Object {
            // None offered.
          }
        }(),
      if (tools)
        () async {
          try {
            refs = (await api.chatRefs()).refs;
          } on Object {
            // None offered.
          }
        }(),
    ]);
    _changed();
  }

  /// refs.ts productRefs: the business's own products (not their sizes).
  List<TagSuggestion> get ownProducts => productRefs(ownStock);

  /// refs.ts knownRefs: the things its words may name with "#".
  List<KnownRef> get known => [
    for (final r in refs) (kind: r.kind, id: r.id, name: r.name),
    for (final p in ownProducts) (kind: 'product', id: p.id.replaceFirst('own:', ''), name: p.name),
  ];

  /// TaskiemChat.tsx briefs: the briefing card is for those who sell or bill.
  bool get briefs => chats.chatTools && chats.auth.can('storefront.manage|invoice.manage');

  /// How often the briefing was asked back: its card is drawn afresh (a
  /// card put away with Got it comes back).
  int briefingShown = 0;

  /// ChatStaff.tsx show: the briefing asked for by name, even put away
  /// for the day (GET /v1/chat-briefing?show=1). False when it did not come.
  Future<bool> showBriefing() async {
    try {
      final b = await api.getChatBriefing(show: '1');
      if (_disposed) return true;
      briefing = b;
      briefingShown++;
      _changed();
      return true;
    } on Object {
      return false;
    }
  }

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

  /// There are older messages than the first page's (TaskiemChat.tsx).
  bool hasOlder = false;
  bool _loadingOlder = false;

  /// The page before the oldest shown.
  Future<void> older() async {
    final first = messages?.where((m) => !m.local).firstOrNull;
    if (first == null || _loadingOlder) return;
    _loadingOlder = true;
    try {
      final out = await api.taskiemHistory(before: first.m.createdAt.toUtc().toIso8601String());
      if (_disposed) return;
      if (out.messages.length < _page) hasOlder = false;
      if (out.messages.isNotEmpty) messages = mergeTaskiem(messages ?? const [], out.messages);
      _changed();
    } on ApiError catch (e) {
      error = e.message;
      _changed();
    } on Offline {
      error = 'You’re offline. The conversation shows when you’re back online.';
      _changed();
    } finally {
      _loadingOlder = false;
    }
  }

  Future<void> open() async {
    unawaited(loadCards());
    unawaited(loadTags());
    try {
      final out = await api.taskiemHistory();
      hasOlder = out.messages.length >= _page;
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
    // Closed before the history came: nothing to follow.
    if (!follow || _disposed) return;
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
    // Given back only while the screen is there to take it.
    if (reply == null && !_giveBack.isClosed) _giveBack.add(mine.m.text);
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
  const TaskiemScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers(), this.recorder, this.onOpenCustomer, this.onOpenLink});

  final TaskiemData data;
  final VoidCallback? onBack;

  /// A customer's conversation, in this one's place (an alert's "Open the
  /// order": the customer's thread, with the order on its card).
  final ValueChanged<String>? onOpenCustomer;

  /// A business's conversation, in this one's place (one linked from its
  /// Explore card).
  final ValueChanged<LinkConversation>? onOpenLink;
  final Pickers pickers;
  final Recorder? recorder;

  @override
  State<TaskiemScreen> createState() => _TaskiemScreenState();
}

class _TaskiemScreenState extends State<TaskiemScreen> {
  final _text = TextEditingController();
  StreamSubscription<String>? _back;

  // TaskiemChat.tsx: with chat tools "@" tags things and businesses in
  // groups and finds businesses not yet linked; Taskiem is who is being
  // talked to, so it is not offered. "#" the business's own products, then
  // what suppliers sell; "/invoice" and the like what the person may see.
  late final _tags = TagBox(
    text: _text,
    sources: _tagSources,
    grouped: () => widget.data.chats.chatTools,
    taskiem: false,
    onChange: () {
      if (mounted) setState(() {});
    },
    explore: (q) async => (await widget.data.chats.api.explore(q: q)).results,
    onExplore: (c) => showExploreCard(context, widget.data.chats, c, onOpen: widget.onOpenLink),
  );

  // The tools panel (ChatTools.tsx): every tool the person may open.
  late final _tools = ToolsController(chats: widget.data.chats)..clearSlash = _text.clear;

  void _openTool(Tool tool, {String? openId}) {
    _tools.close();
    // Sell, the handover and availability have sheets of their own: not a
    // list.
    if (tool.key == 'availability') {
      unawaited(showAvailabilitySheet(context, auth: widget.data.chats.auth, onChanged: () => _tools.load([tool], again: true)));
      return;
    }
    // Sell and Handover have sheets of their own (ChatStaff.tsx); what
    // they record lands here as a diary line.
    void landed() {
      unawaited(widget.data.pull().catchError((Object _) {}));
      unawaited(widget.data.chats.load());
    }

    if (tool.key == 'sell') {
      unawaited(showSellSheet(context, auth: widget.data.chats.auth, onSold: landed));
      return;
    }
    if (tool.key == 'handover') {
      unawaited(showHandoverSheet(context, auth: widget.data.chats.auth, onSent: landed));
      return;
    }
    showToolSheet(
      context,
      tools: _tools,
      tool: tool,
      detail: toolDetail(widget.data.chats.auth),
      openId: openId,
      // Saved: its diary line here at once, and the alerts read again.
      onSaved: (_) {
        unawaited(widget.data.pull().catchError((Object _) {}));
        unawaited(widget.data.chats.load());
      },
    );
  }

  /// TaskiemChat.tsx extras: the briefing, tasks, and clocking in and out.
  List<ExtraTile> _extras(TaskiemData d) {
    final can = d.chats.auth.can;
    void pick(VoidCallback f) {
      _tools.close();
      f();
    }

    return [
      if (d.briefs) (key: 'briefing', title: 'Briefing', icon: 'sparkle', line: "Today's numbers", onPick: () => pick(() => _showBriefing(d))),
      // Tasks, given and done: its sheet is not in the app yet.
      (key: 'tasks', title: 'Tasks', icon: 'check', line: 'To do, and given', onPick: () => pick(() => showComingSoon(context))),
      // Clocking in and out, as "in" and "out" are said: for staff.
      if (!can('member.manage')) ...[
        (key: 'clock-in', title: 'Clock in', icon: 'clock', line: 'Start your shift', onPick: () => pick(() => d.say(text: 'in'))),
        (key: 'clock-out', title: 'Clock out', icon: 'clock', line: 'End your shift', onPick: () => pick(() => d.say(text: 'out'))),
      ],
    ];
  }

  TagSources _tagSources() {
    final d = widget.data;
    final own = d.ownProducts;
    final now = d.chats.now();
    return (
      items: [
        ...own,
        for (final p in d.suppliers)
          for (final it in p.items)
            if (it.available)
              TagSuggestion(
                kind: 'item',
                id: '${p.link}:${it.itemId}',
                name: it.name,
                sub: '${p.business} · ${priceWords(it)}',
                category: it.category,
                words: it.description,
              ),
      ],
      services: const [],
      businesses: [
        for (final x in d.chats.conversations)
          if (x.state == 'active') TagSuggestion(kind: 'business', id: x.link, name: x.name),
      ],
      refs: {
        ...refSources([for (final r in d.refs) refTag(r, now, date: shortDate, status: refStatusWords)]),
        'product': own,
      },
    );
  }

  @override
  void initState() {
    super.initState();
    // The words left unsent here, back in the box, and kept as they change.
    _text.text = widget.data.chats.draft('taskiem');
    _text.addListener(() => widget.data.chats.setDraft('taskiem', _text.text));
    _text.addListener(_tags.typed);
    // "/" and a word as the whole message: the panel, filtered by it.
    _text.addListener(() => _tools.typed(_tags.query?.mark == '/' ? '' : _text.text));
    if (widget.data.messages == null) widget.data.open();
    widget.data.chats.viewingTaskiem = true;
    _back = widget.data.givenBack.listen((words) {
      if (_text.text.isEmpty) _text.text = words;
    });
  }

  @override
  void dispose() {
    _tools.dispose();
    widget.data.chats.viewingTaskiem = false;
    _back?.cancel();
    _tags.dispose();
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
        child: WallpaperBox(
          ground: t.chatWallpaper,
          doodle: t.chatDoodle,
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
                _tags.plain(),
                _tags.anchor(context),
                ToolsAnchor(
                  tools: _tools,
                  panel: (context, room) =>
                      ToolsPanel(tools: _tools, onOpen: _openTool, extras: _extras(d), recent: d.chats.prefs?.recentTools ?? const [], maxHeight: room),
                ),
                Composer(
                  tools: d.chats.chatTools ? _tools : null,
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
                  onSend: (words, file) {
                    // Tags name things for Taskiem in the words alone.
                    _tags.take(words);
                    if (file != null) {
                      d.sendFile(file, words);
                    } else if (d.briefs && asksBriefing(words)) {
                      // Asked for by name: the card, not a message.
                      _showBriefing(d);
                    } else {
                      d.say(text: words);
                    }
                  },
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  final _threadKey = GlobalKey<ThreadViewState<TaskiemLine>>();

  Future<void> _showBriefing(TaskiemData d) async {
    final ok = await d.showBriefing();
    if (!mounted) return;
    if (!ok) {
      showTkToast(context, 'The briefing didn\'t load. Try again.', error: true);
      return;
    }
    // Into sight: it is after the messages, at the end.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final s = _threadKey.currentState?.scroll;
      if (s != null && s.hasClients) s.jumpTo(s.position.maxScrollExtent);
    });
  }

  Widget _thread(BuildContext context, TaskiemData d) {
    final t = TkTheme.tokens(context);
    final known = d.known;
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
        key: _threadKey,
        items: list,
        of: (id: (x) => x.id == nilId ? '${x.id}-${x.m.createdAt}' : x.id, at: (x) => x.m.createdAt, mine: (x) => x.m.inbound),
        now: d.chats.now(),
        unread: d.unread,
        hasOlder: d.hasOlder,
        onOlder: d.older,
        tail: d.briefing == null && d.alerts.isEmpty
            ? null
            : Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (d.briefing != null) ...[
                    BriefingCard(
                      key: ValueKey(d.briefingShown),
                      briefing: d.briefing!,
                      auth: d.chats.auth,
                      onAsk: (w) => d.say(text: w),
                      onTile: (key) {
                        final auth = d.chats.auth;
                        void push(Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));
                        switch (key) {
                          case 'stock':
                            final tool = tools.where((x) => x.key == 'stock').firstOrNull;
                            if (tool != null) _openTool(tool);
                          case 'deliveries':
                            push(DeliveriesPage(auth: auth));
                          case 'bookings':
                            push(BookingsPage(auth: auth));
                          default:
                            // Invoices (M8), staff and tasks: coming.
                            showComingSoon(context);
                        }
                      },
                    ),
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
                    onOpen: (what, a) {
                      final tool = tools.where((x) => x.key == what).firstOrNull;
                      if (what == 'customer' && (a.id ?? '').isNotEmpty) {
                        widget.onOpenCustomer?.call(a.id!);
                      } else if (what == 'rider' && (a.reference ?? '').isNotEmpty) {
                        // Assign rider: the next in turn, or one by name.
                        unawaited(
                          showRiderSheet(
                            context,
                            auth: d.chats.auth,
                            reference: a.reference!,
                            line: a.line,
                            onDone: () {
                              d.loadCards();
                              unawaited(d.pull().catchError((Object _) {}));
                            },
                          ),
                        );
                      } else if (tool != null && (what == 'orders' || what == 'stock')) {
                        // Review order, Record stock in: the row in its tool's sheet.
                        _openTool(tool, openId: a.id);
                      } else {
                        // Its sheet comes with M8 (reorder, match).
                        showComingSoon(context);
                      }
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
              onLink: (u) => openTaskiemLink(context, d.chats.auth, u, onDone: d.pull),
              known: known,
              auth: d.chats.auth,
              now: d.chats.now(),
              onCustomer: widget.onOpenCustomer,
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
  const _TaskiemBubble({
    required this.x,
    required this.first,
    required this.read,
    required this.choice,
    required this.pressable,
    required this.onPress,
    this.onLink,
    this.known = const [],
    this.auth,
    this.now,
    this.onCustomer,
  });

  /// What its words may name with "#" (refs.ts knownRefs).
  final List<KnownRef> known;
  final Auth? auth;
  final DateTime? now;

  /// A customer named: their conversation.
  final ValueChanged<String>? onCustomer;

  /// A connect link or a workflow's canvas in its words, pressed.
  final ValueChanged<Uri>? onLink;
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
    // A connect link or a workflow's canvas (§3.5) is pressed to open it;
    // other addresses stay words, as on the web.
    List<InlineSpan> linked(String text, TextStyle? style, TapGestureRecognizer Function(String, VoidCallback) tap) {
      final links = widget.onLink == null ? const <({int start, int end, Uri uri})>[] : linksIn(text).where((l) => opensFromTaskiem(l.uri)).toList();
      if (links.isEmpty) return [TextSpan(text: text, style: style)];
      final out = <InlineSpan>[];
      var at = 0;
      for (final l in links) {
        if (l.start > at) out.add(TextSpan(text: text.substring(at, l.start), style: style));
        out.add(
          TextSpan(
            text: text.substring(l.start, l.end),
            style: (style ?? const TextStyle()).copyWith(color: t.accent, decoration: TextDecoration.underline, decorationColor: t.accent),
            recognizer: tap(l.uri.toString(), () => widget.onLink!(l.uri)),
          ),
        );
        at = l.end;
      }
      if (at < text.length) out.add(TextSpan(text: text.substring(at), style: style));
      return out;
    }

    // TaskiemText: a thing it knows, named with "#", marked; a customer
    // opens their conversation; the rest open in the tools (M5 to M8).
    List<InlineSpan> part(String text, TextStyle? style, TapGestureRecognizer Function(String, VoidCallback) tap) {
      final out = <InlineSpan>[];
      for (final r in refParts(text, widget.known)) {
        final ref = r.ref;
        if (ref == null) {
          out.addAll(linked(r.text, style, tap));
          continue;
        }
        final mention = (style ?? const TextStyle()).copyWith(
          color: t.tintText,
          fontWeight: FontWeight.w600,
          fontVariations: const [FontVariation('wght', 600)],
        );
        // TaskiemChat.tsx: an order opens on its own screen, by its reference.
        final open = ref.kind == 'customer'
            ? widget.onCustomer
            : ref.kind == 'order' && widget.auth != null
            ? (String _) => openStorefrontOrder(context, widget.auth!, ref.name)
            : null;
        // Anything else opens in its tool, which comes with the tools.
        out.add(
          TextSpan(text: r.text, style: mention, recognizer: tap('ref:${ref.kind}:${ref.id}', () => open == null ? showComingSoon(context) : open(ref.id))),
        );
      }
      return out;
    }

    final words = TapsOwner(
      builder: (context, tap) => Text.rich(
        TextSpan(
          children: [
            for (final p in boldParts(fold.shown))
              ...part(p.text, p.bold ? const TextStyle(fontWeight: FontWeight.w700, fontVariations: [FontVariation('wght', 700)]) : null, tap),
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
      ),
    );
    // TaskiemChat.tsx: a message's menu holds Copy (not one still going).
    final copyable = !widget.x.local && m.text.isNotEmpty;
    return BubbleHold(
      label: mine ? 'Your message' : 'Message from Taskiem',
      onHold: copyable
          ? (at) => showMessageMenu(
              context,
              bubble: at,
              mine: mine,
              what: m.text.split('\n').first.length > 60 ? m.text.split('\n').first.substring(0, 60) : m.text.split('\n').first,
              items: [MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: m.text)), done: 'Copied')],
            )
          : null,
      child: BubbleFrame(
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
          // The documents it names, each as its card, read now.
          for (final r in cardRefs(m.text, widget.known))
            RefCard(
              key: ValueKey('${r.kind}:${r.id}'),
              auth: widget.auth!,
              now: widget.now ?? DateTime.now(),
              load: () => widget.auth!.api.chatCard(kind: r.kind, id: r.id),
              onMessage: widget.onCustomer,
            ),
          if (buttons.isNotEmpty) _ReplyButtons(buttons: buttons, choice: widget.choice, pressable: widget.pressable, onPress: widget.onPress),
        ],
      ),
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
                            onTap: () => onPress(b),
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
