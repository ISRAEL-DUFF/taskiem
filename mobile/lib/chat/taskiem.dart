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
import '../app/links.dart' show openInBrowser;
import '../kit/motion.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'alerts.dart';
import 'attachments.dart';
import 'cards.dart';
import 'chat_link_page.dart' show showCustomersSheet;
import 'chats.dart';
import 'composer.dart';
import 'connect.dart';
import 'live.dart';
import 'made_tools.dart';
import 'explore.dart' show showExploreCard;
import 'menu.dart';
import 'ref_card.dart';
import 'saved_picks.dart';
import 'tag_box.dart';
import 'tags.dart';
import 'thread.dart';
import 'tool_forms.dart' show toolDetail;
import 'sell_sheet.dart' show showHandoverSheet, showSellSheet;
import 'availability_sheet.dart' show showAvailabilitySheet;
import 'bookings_page.dart' show BookingsPage;
import 'invoice_match.dart' show InvoiceMatchPage, showMatchSheet;
import 'bills_page.dart' show BillsPage, openBill;
import 'menu_card.dart';
import 'money_page.dart' show MoneyPage;
import 'payments_page.dart' show SentPaymentsPage;
import 'pay_diary.dart' show PayChatCard, PayDiaryLine;
import 'payroll_page.dart' show PayrollPage;
import 'payroll_run.dart' show PayrollChatCard, payrollCardOf;
import 'payslip.dart' show PayslipChatCard, payslipCardOf;
import 'pages_page.dart' show PagesPage, openPage;
import 'recipes.dart' show openRecipes;
import 'saved_accounts.dart' show SavedAccountsPage;
import 'invoices_page.dart' show InvoicesPage, openInvoice;
import 'purchases_page.dart' show openPurchase;
import 'order_sheet.dart' show OrderStart, showOrderSheet;
import 'team_pages.dart' show StaffPage, TasksPage, openTasks;
import 'storefront_orders_page.dart' show DeliveriesPage, openStorefrontOrder, showRiderSheet;
import 'tool_sheet.dart';
import 'tools.dart';
import 'tools_panel.dart';
import 'words.dart';
import 'save_as.dart' show saveFromWords, saveNamed;
import 'saved_files.dart' show attachSavedFile;
import '../kit/mobile.dart' show TkRoute, afterSlideIn;
import '../school/school_chasing.dart' show DebtorsPage;
import '../school/school_clearance.dart' show ClearancePage;
import '../school/school_money.dart' show PaymentsPage;
import '../school/students_page.dart' show StudentsPage;

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
  TaskiemLine(this.m, {this.local = false, this.run, this.echo = false});
  final TaskiemMessage m;
  final bool local;

  /// One of the business's tools run from here (made tools M1): what the
  /// person sent it ([echo]), or its answer.
  final MadeRun? run;
  final bool echo;
  String get id => m.id;
}

/// taskiemchat.ts mergeTaskiem: messages merged by id, oldest first; one
/// not kept (a code) stays as sent. A message of ours being sent gives way
/// to its stored copy when that comes first (by the stream); the copy of an
/// answer that carries its buttons' ids is kept over one that does not.
List<TaskiemLine> mergeTaskiem(List<TaskiemLine> a, List<TaskiemMessage> b) {
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
            TaskiemMessage(
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
  bool get briefs => chats.chatTools && chats.auth.can('storefront.manage|invoice.manage|school.manage');

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
      chats.auth.local?.write('taskiem_alerts', [for (final a in alerts) a.toJson()]);
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

  /// Offline: what shows is what the phone kept.
  bool offline = false;

  /// Taskiem's conversation as the phone kept it: shown at once.
  void _restore() {
    final local = chats.auth.local;
    if (local == null) return;
    try {
      final kept = [for (final j in local.messages('taskiem')) TaskiemMessage.fromJson(j)];
      if (kept.isNotEmpty && messages == null) {
        final list = mergeTaskiem(const [], kept);
        unread = unreadAfter(list, chats.taskiemSeen);
        messages = list;
        hasOlder = true;
      }
      final cards = local.read('taskiem_alerts');
      if (cards is List && alerts.isEmpty) alerts = [for (final c in cards) ChatAlert.fromJson((c as Map).cast<String, dynamic>())];
      _changed();
    } on Object {
      // Kept by an older app, or damaged: the server's answer will do.
    }
  }

  void _keep() {
    final local = chats.auth.local;
    final list = messages?.where((x) => !x.local).toList();
    if (local == null || list == null) return;
    try {
      local.keepMessages('taskiem', [for (final x in list.length > 200 ? list.sublist(list.length - 200) : list) x.m.toJson()]);
    } on Object {
      // Not kept this time.
    }
  }

  Future<void> open() async {
    _restore();
    unawaited(loadCards());
    try {
      final out = await api.taskiemHistory();
      hasOlder = out.messages.length >= _page;
      final list = mergeTaskiem(const [], out.messages);
      unread = unreadAfter(list, chats.taskiemSeen);
      messages = list;
      offline = false;
      _keep();
      _changed();
    } on ApiError catch (e) {
      error = e.message;
      _changed();
    } on Offline {
      if (messages != null) {
        offline = true;
      } else {
        error = 'You’re offline. The conversation shows when you’re back online.';
      }
      _changed();
    }
    // Closed before the history came: nothing to follow.
    if (!follow || _disposed) return;
    _live ??= chats.live?.events.listen((e) {
      if (e is LiveTaskiem) {
        _merge(e.messages.map(TaskiemMessage.fromJson).toList());
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
    final TaskiemChat out;
    try {
      out = await api.taskiemHistory(after: after?.toUtc().toIso8601String());
    } on Offline {
      if (!offline && !_disposed) {
        offline = true;
        _changed();
      }
      return;
    }
    if (offline && !_disposed) {
      offline = false;
      _changed();
    }
    if (out.messages.isEmpty) return;
    _merge(out.messages);
  }

  void _merge(List<TaskiemMessage> add) {
    if (_disposed) return;
    messages = mergeTaskiem(messages ?? const [], add);
    _keep();
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
        TaskiemMessage(id: 'local-${++_localN}', channel: 'web', inbound: true, kind: 'text', text: words, createdAt: chats.now()),
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

/// A tool to open as the conversation opens, its form filled in (a
/// shortcut on the app's icon, a code scanned from the home screen).
typedef ToolToOpen = ({String command, Map<String, dynamic> initial});

/// Taskiem's conversation.
class TaskiemScreen extends StatefulWidget {
  const TaskiemScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers(), this.recorder, this.onOpenCustomer, this.onOpenLink, this.openTool});

  /// Opened at once, once the person's tools are read.
  final ToolToOpen? openTool;

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
    // Invoices, orders and customers searched on the server, every record
    // (menus-everywhere.md §7).
    searchRefs: (words, kinds, cursor) async {
      final r = await widget.data.api.chatRefs(q: words, kind: kinds, cursor: cursor, limit: '20');
      final now = widget.data.chats.now();
      return (list: [for (final x in r.refs) refTag(x, now, date: shortDate, status: refStatusWords)], cursor: r.cursor ?? '');
    },
    grouped: () => widget.data.chats.chatTools,
    taskiem: false,
    onChange: () {
      if (mounted) setState(() {});
    },
    explore: (q) async => (await widget.data.chats.api.explore(q: q)).results,
    onExplore: (c) => showExploreCard(context, widget.data.chats, c, onOpen: widget.onOpenLink),
    // Saved things by @name (saved objects S1): Taskiem reads any of them.
    saved: SavedPicks(
      auth: widget.data.chats.auth,
      textsOnly: false,
      onChange: () {
        if (mounted) setState(() {});
      },
    ),
    onSaveNamed: (name) => unawaited(saveNamed(context, widget.data.chats.auth, name, business: true)),
    onAttachSaved: (s) => unawaited(attachSavedFile(context, widget.data.chats.auth, s, (f) => _composer.currentState?.choose(f))),
  );
  final _composer = GlobalKey<ComposerState>();

  // The tools panel (ChatTools.tsx): every tool the person may open.
  late final _tools = ToolsController(chats: widget.data.chats)..clearSlash = _text.clear;

  // Your tools' runs from here (made tools M1), each read until it ends.
  late final _runs = MadeRuns(widget.data.chats.auth, now: widget.data.chats.now);

  /// A tool of the business's own: its sheet, then its run here, its
  /// answer when it ends.
  /// True when a run was started.
  Future<bool> _openMade(MadeTool tool, {Map<String, dynamic> initial = const {}, String? button, String? words}) async {
    _tools.close();
    unawaited(widget.data.chats.prefs?.rememberTool(madeKey(tool)));
    final got = await showMadeToolSheet(
      context,
      auth: widget.data.chats.auth,
      tool: tool,
      initial: initial,
      words: words,
      button: button,
      onAlwaysAsk: (on) => _tools.setAlwaysAsk(tool.command, on),
    );
    if (got == null || !mounted) return false;
    final run = MadeRun(id: got.run, command: tool.command, name: tool.name, values: got.values, at: widget.data.chats.now());
    _runs.add(run);
    _reveal(run);
    return true;
  }

  /// The tool the conversation was opened for (the app icon's shortcut, a
  /// code scanned from the home screen): its sheet, filled in.
  void _openGiven() {
    final given = widget.openTool;
    if (given == null || !mounted) return;
    final tool = _tools.made.where((t) => t.command == given.command).firstOrNull;
    if (tool == null) {
      showTkToast(context, 'That tool isn’t one you can use here.', error: true);
      return;
    }
    unawaited(_openMade(tool, initial: given.initial));
  }

  /// Where each run's answer is drawn, to bring it into sight.
  final _runKeys = <String, GlobalKey>{};

  /// Those ended, so one that ends is brought into sight once.
  late final _ended = {for (final r in _runs.runs) if (!r.going) r.id};

  /// A run's answer into sight: it sits among the messages, before the
  /// cards that follow them.
  void _reveal(MadeRun r) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final at = _runKeys[r.id]?.currentContext;
      if (at == null || !at.mounted) {
        final s = _threadKey.currentState?.scroll;
        if (s != null && s.hasClients) s.jumpTo(s.position.maxScrollExtent);
        return;
      }
      unawaited(Scrollable.ensureVisible(at, alignment: 0.95, duration: const Duration(milliseconds: 250), curve: Tk.easeOut));
    });
  }

  void _runsChanged() {
    for (final r in _runs.runs) {
      if (!r.going && _ended.add(r.id)) _reveal(r);
    }
  }

  /// A button on an answer, pressed by its token (made tools M2): the
  /// tool the server names, its form filled with the values it gives, the
  /// start carrying the button. A once button greys when its run starts,
  /// or when someone was first (said in the server's words).
  Future<void> _answerButton(MadeRun run, ToolButton b, String key) async {
    final token = b.token;
    if (token == null || token.isEmpty || _pressing) return;
    _pressing = true;
    PressToolButtonResponse p;
    try {
      p = await widget.data.chats.auth.api.pressToolButton(button: token);
    } on Object catch (e) {
      final no = pressRefused(e);
      if (no.spent) _runs.pressed(run, key);
      if (mounted) showTkToast(context, no.words, error: true);
      return;
    } finally {
      _pressing = false;
    }
    if (!mounted) return;
    final tool = _tools.made.where((t) => t.command == p.command).firstOrNull;
    if (tool == null) {
      showTkToast(context, 'That tool isn’t one you can use here.', error: true);
      return;
    }
    final started = await _openMade(tool, initial: p.inputs, button: token);
    if (started && p.once) _runs.pressed(run, key);
  }

  /// A button's press on its way: one at a time.
  bool _pressing = false;

  /// One of the business's pages, over the chat (its tile, or "/" and its name).
  void _openOnePage(String name) {
    _tools.close();
    unawaited(openPage(context, widget.data.chats.auth, name, backLabel: 'Back to Taskiem'));
  }

  /// A menu row's page, where the app has it as a screen.
  bool _openPage(String path) {
    final auth = widget.data.chats.auth;
    final Widget? page = switch (path) {
      '/bills' => BillsPage(auth: auth),
      '/invoices' => InvoicesPage(auth: auth),
      '/money' => MoneyPage(auth: auth),
      '/schools' || '/schools/payments' => StudentsPage(auth: auth),
      '/schools/debtors' => DebtorsPage(auth: auth),
      '/schools/clearance' => ClearancePage(auth: auth),
      '/payments' => SentPaymentsPage(auth: auth, backLabel: 'Back to Taskiem'),
      '/payroll' => PayrollPage(auth: auth, backLabel: 'Back to Taskiem'),
      '/pages' => PagesPage(auth: auth, backLabel: 'Back to Taskiem'),
      '/settings/saved' => SavedAccountsPage(auth: auth, mine: false),
      _ => null,
    };
    if (page == null) return false;
    unawaited(Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page)));
    return true;
  }

  void _openTool(Tool tool, {String? openId, bool add = false}) {
    _tools.close();
    // Sell, the handover and availability have sheets of their own: not a
    // list.
    // Sell, Handover and Availability have sheets of their own; what they
    // record lands here as a diary line.
    void landed() {
      unawaited(widget.data.pull().catchError((Object _) {}));
      unawaited(widget.data.chats.load());
    }

    if (tool.key == 'availability') {
      unawaited(
        showAvailabilitySheet(
          context,
          auth: widget.data.chats.auth,
          onChanged: () {
            // Changed: read now, not only once five seconds old.
            _tools.refresh(tool);
            landed();
          },
        ),
      );
      return;
    }

    // Customers: your chat link and an offer, in a sheet of their own.
    if (tool.key == 'customers') {
      unawaited(showCustomersSheet(context, widget.data.chats.auth));
      return;
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
      add: add,
      // Saved: its diary line here at once, and the alerts read again.
      onSaved: (_) {
        unawaited(widget.data.pull().catchError((Object _) {}));
        unawaited(widget.data.chats.load());
      },
    );
  }

  /// A school's tools, first (as drawn): its students, cash at the desk,
  /// debtors and clearance, each its School page.
  List<ExtraTile> _schoolTiles(TaskiemData d) {
    final auth = d.chats.auth;
    if (auth.me?.schools != true || !auth.can('school.read')) return const [];
    void go(Widget page) {
      _tools.close();
      Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));
    }

    return [
      (key: 'school-students', title: 'Students', icon: 'b-person', line: 'Their balance, their account', onPick: () => go(StudentsPage(auth: auth))),
      if (auth.can('school.manage'))
        (key: 'school-payment', title: 'Record a payment', icon: 'b-fees', line: 'Cash at the desk', onPick: () => go(StudentsPage(auth: auth))),
      (key: 'school-debtors', title: 'Debtors', icon: 'b-hand', line: 'Reminders, promises', onPick: () => go(DebtorsPage(auth: auth))),
      (key: 'school-clearance', title: 'Clearance', icon: 'b-gate', line: 'Who may sit exams', onPick: () => go(ClearancePage(auth: auth))),
    ];
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
      // Tasks, given and done: the Tasks screen.
      (key: 'tasks', title: 'Tasks', icon: 'check', line: 'To do, and given', onPick: () => pick(() => openTasks(context, d.chats.auth))),
      // Ready-made tools (made tools §13): recipes to add, for builders.
      if (can('workflow.edit'))
        (key: 'recipes', title: 'Add a tool', icon: 'plus', line: 'Ready-made', onPick: () => pick(() => unawaited(openRecipes(context, d.chats.auth, backLabel: 'Back to Taskiem')))),
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
    _text.addListener(() => _tools.typed(_tags.tagging ? '' : _text.text));
    if (widget.data.messages == null) widget.data.open();
    // The business's own tools, read now: the tools button shows for them
    // even with the built-in tools off.
    // The rest once it has slid in: the slide has the phone to itself.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      afterSlideIn(context, () {
        if (!mounted) return;
        unawaited(widget.data.loadTags());
        unawaited(_tools.loadMade().then((_) => _openGiven()));
        unawaited(_tools.loadPages());
      });
    });
    _runs.addListener(_runsChanged);
    // The menus' catalogue, read now, so a menu is drawn whole at once.
    unawaited(catalogueOf(widget.data.chats.auth).then((_) {}, onError: (Object _) {}));
    widget.data.chats.viewingTaskiem = true;
    _back = widget.data.givenBack.listen((words) {
      if (_text.text.isEmpty) _text.text = words;
    });
  }

  @override
  void dispose() {
    _sel.dispose();
    _tools.dispose();
    _runs.dispose();
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
            listenable: Listenable.merge([d, _runs]),
            builder: (context, _) => Column(
              children: [
                ColoredBox(
                  // The head on a bar of its own, as WhatsApp's (under the phone's
                  // status bar too), apart from the wallpaper.
                  color: t.panel,
                  child: Padding(
                    padding: EdgeInsets.only(top: pad.top),
                    child: SelectionHead(selection: _sel, child: ChatHeadBar(
                      onBack: widget.onBack,
                      avatar: Container(
                        width: 42,
                        height: 42,
                        decoration: BoxDecoration(color: t.brand, shape: BoxShape.circle),
                        alignment: Alignment.center,
                        child: TkTaskiemMark(size: 20, color: t.brandDark),
                      ),
                      title: 'Taskiem',
                      subtitle: d.offline ? 'Waiting for network…' : (d.chats.auth.me?.tenantName ?? 'Your assistant'),
                      typing: d.busy,
                    )),
                  ),
                ),
                Expanded(child: _thread(context, d)),
                _tags.plain(),
                _tags.anchor(context),
                ToolsAnchor(
                  tools: _tools,
                  panel: (context, room) =>
                      ToolsPanel(
                        tools: _tools,
                        onOpen: _openTool,
                        onMade: (x) => unawaited(_openMade(x)),
                        onPage: (p) => _openOnePage(p.name),
                        // Taskiem's own tiles come with the built-in tools.
                        lead: d.chats.chatTools ? _schoolTiles(d) : const [],
                        extras: d.chats.chatTools ? _extras(d) : const [],
                        recent: d.chats.prefs?.recentTools ?? const [],
                        maxHeight: room,
                        // The topics' menu (menus-everywhere.md §4): a row
                        // pressed here is as pressed in a menu of the chat.
                        menu: (context) => MenuCard(
                          auth: d.chats.auth,
                          menu: const MenuMessage(),
                          open: true,
                          head: false,
                          busy: d.busy,
                          onAct: (a) {
                            _tools.close();
                            unawaited(d.say(reply: 'act:${a.id}', text: a.title));
                          },
                          onTool: (key, add) {
                            final tool = tools.where((t) => t.key == key).firstOrNull;
                            if (tool == null) return false;
                            _openTool(tool, add: add);
                            return true;
                          },
                          onPage: (path) {
                            _tools.close();
                            return _openPage(path);
                          },
                          onForm: (reply, text) {
                            _tools.close();
                            unawaited(d.say(reply: reply, text: text));
                          },
                        ),
                      ),
                ),
                // A sentence starting with a phrase one of the person's
                // tools says: "Open X, filled in" over the box (the web's
                // chip). Sent instead, the words go to the chat.
                ListenableBuilder(
                  listenable: Listenable.merge([_tools, _text]),
                  builder: (context, _) {
                    final r = _tools.shown || _tags.tagging ? null : madeByPhrase(_tools.made, _text.text);
                    if (r == null) return const SizedBox.shrink();
                    return MadeToolChip(
                      tool: r.tool,
                      words: r.words,
                      onOpen: () {
                        _text.clear();
                        unawaited(_openMade(r.tool, words: r.words.isEmpty ? null : r.words));
                      },
                    );
                  },
                ),
                ListenableBuilder(
                  listenable: _tools,
                  builder: (context, _) => Composer(
                    key: _composer,
                    tools: d.chats.chatTools || _tools.made.isNotEmpty || _tools.pages.isNotEmpty ? _tools : null,
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
                      final made = file == null ? madeByCommand(_tools.made, words) : null;
                      final page = file == null && made == null ? _tools.pageCalled(words) : null;
                      if (file != null) {
                        d.sendFile(file, words);
                      } else if (page != null) {
                        // "/tickets": the page opens over the chat (pages §9).
                        _text.clear();
                        _openOnePage(page.name);
                      } else if (made != null) {
                        // "/expense 4500 diesel": its form, filled in from
                        // the words; nothing starts before its own button.
                        unawaited(_openMade(made.tool, words: made.words));
                      } else if (d.briefs && asksBriefing(words)) {
                        // Asked for by name: the card, not a message.
                        _showBriefing(d);
                      } else {
                        d.say(text: words);
                      }
                    },
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  final _threadKey = GlobalKey<ThreadViewState<TaskiemLine>>();

  /// What the person sent a tool, as their message: its name, then every
  /// value as the server read it (the echo of made tools §8), or its
  /// command when it took none.
  static String _echoWords(MadeRun r) => ['*${r.name}*', if (r.values.isEmpty) '/${r.command}', for (final v in r.values) '${v.$1}: ${v.$2}'].join('\n');

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

  /// A line's id in the thread.
  static String _idOf(TaskiemLine x) => x.id == nilId ? '${x.id}-${x.m.createdAt}' : x.id;

  /// The thread's lines as last drawn.
  List<TaskiemLine>? _held;

  /// The messages held (WhatsApp's selection, menu.dart).
  late final _sel = MessageSelection(actsOf: _actsOf);

  /// TaskiemChat.tsx: a message's menu holds Copy (not one still going),
  /// and Save as….
  HeldActs? _actsOf(String id) {
    final x = _held?.where((x) => _idOf(x) == id).firstOrNull;
    if (x == null || x.local || x.m.text.isEmpty) return null;
    final m = x.m;
    final auth = widget.data.chats.auth;
    final first = m.text.split('\n').first;
    return HeldActs(
      what: first.length > 60 ? first.substring(0, 60) : first,
      mine: m.inbound,
      words: m.text,
      more: [
        // Save as… (saved objects S1, part 4): in a business, its saved things for whoever may manage them.
        MenuItem('note', 'Save as…', () => saveFromWords(context, auth, m.text, business: true)),
      ],
    );
  }

  Widget _thread(BuildContext context, TaskiemData d) {
    final t = TkTheme.tokens(context);
    final known = d.known;
    // Your tools' runs among the messages: what the person sent each, then
    // its answer; the history's own copy of an answer left out.
    final runs = _runs.runs;
    final list = d.messages == null
        ? null
        : runs.isEmpty
        ? d.messages
        : ([
            for (final x in d.messages!)
              if (x.m.inbound || !runs.any((r) => isAnswerCopy(r, x.m.text, x.m.createdAt))) x,
            for (final r in runs) ...[
              TaskiemLine(
                TaskiemMessage(id: 'run-${r.id}-echo', channel: 'web', inbound: true, kind: 'text', text: _echoWords(r), createdAt: r.at),
                run: r,
                echo: true,
              ),
              TaskiemLine(
                TaskiemMessage(
                  id: 'run-${r.id}',
                  channel: 'web',
                  inbound: false,
                  kind: 'text',
                  text: runWordsOf(r) ?? '',
                  createdAt: r.at.add(const Duration(milliseconds: 1)),
                ),
                run: r,
              ),
            ],
          ]..sort((p, q) => p.m.createdAt.compareTo(q.m.createdAt)));
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
    // The newest menu stays open while nothing has been said since it;
    // every other folds (chat-menus.md §6.1; folding once the person types
    // something else, 21 November 2026).
    String? lastMenu;
    for (final x in l.reversed) {
      if (x.m.inbound) break;
      if (x.m.kind == 'menu') {
        lastMenu = x.id;
        break;
      }
    }
    return SelectionScope(selection: _sel, child: NotificationListener<ScrollEndNotification>(
      // Seen once the person is at the newest end.
      onNotification: (n) {
        if (n.metrics.maxScrollExtent - n.metrics.pixels <= 80) d.seen();
        return false;
      },
      child: ThreadView<TaskiemLine>(
        key: _threadKey,
        selection: _sel,
        items: _held = list,
        of: (id: _idOf, at: (x) => x.m.createdAt, mine: (x) => x.m.inbound),
        arrival: (x) => x.m.kind == 'diary'
            ? TkArrival.diary
            : x.m.kind == 'chart' || (x.run != null && !x.echo && runWordsOf(x.run!) == null)
            ? TkArrival.card
            : TkArrival.bubble,
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
                        void push(Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));
                        switch (key) {
                          case 'stock':
                            final tool = tools.where((x) => x.key == 'stock').firstOrNull;
                            if (tool != null) _openTool(tool);
                          case 'deliveries':
                            push(DeliveriesPage(auth: auth));
                          case 'bookings':
                            push(BookingsPage(auth: auth));
                          case 'owed':
                            push(InvoicesPage(auth: auth));
                          case 'staff':
                            push(StaffPage(auth: auth));
                          case 'tasks':
                            push(TasksPage(auth: auth));
                          case 'school:debtors':
                            push(DebtorsPage(auth: auth));
                          case 'school:clearance':
                            push(ClearancePage(auth: auth));
                          case 'school:waiting':
                            push(PaymentsPage(auth: auth, status: 'pending'));
                          case 'school:match':
                            push(InvoiceMatchPage(auth: auth));
                          default:
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
                      } else if (what == 'match' && (a.id ?? '').isNotEmpty) {
                        // Match it: what the money pays.
                        unawaited(
                          showMatchSheet(
                            context,
                            auth: d.chats.auth,
                            entry: a.id!,
                            amount: a.amountKobo,
                            line: a.line,
                            onDone: () {
                              d.loadCards();
                              unawaited(d.pull().catchError((Object _) {}));
                            },
                          ),
                        );
                      } else if (what == 'reorder' && a.reorder != null) {
                        // Reorder: the supplier's shop, the low item (or
                        // its low sizes, in the product's grid) in the bag.
                        final r = a.reorder!;
                        unawaited(
                          showOrderSheet(
                            context,
                            chats: d.chats,
                            link: r.link,
                            business: r.supplier,
                            start: r.sizes != null
                                ? OrderStart(itemId: r.sellerItemId, qty: {for (final e in r.sizes!.entries) e.key: (e.value as num).toInt()})
                                : OrderStart(qty: {r.sellerItemId: r.quantity}),
                            onSent: (_) {
                              d.loadCards();
                              unawaited(d.pull().catchError((Object _) {}));
                            },
                          ),
                        );
                      } else {
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
          // A tool's answer that is more than words: its card.
          if (x.run != null && !x.echo && runWordsOf(x.run!) == null) {
            return Padding(
              key: _runKeys.putIfAbsent(x.run!.id, GlobalKey.new),
              padding: const EdgeInsets.only(top: 6),
              child: MadeAnswerCard(run: x.run!, auth: d.chats.auth, onButton: _answerButton),
            );
          }
          if (m.kind == 'menu') {
            final menu = MenuMessage.parse(m.text);
            if (menu != null) {
              return MenuCard(
                key: ValueKey('menu-${x.id}'),
                auth: d.chats.auth,
                id: x.id,
                menu: menu,
                open: x.id == lastMenu,
                busy: d.busy,
                onAct: (a) => d.say(reply: 'act:${a.id}', text: a.title),
                onTool: (key, add) {
                  final tool = tools.where((t) => t.key == key).firstOrNull;
                  if (tool == null) return false;
                  _openTool(tool, add: add);
                  return true;
                },
                onPage: (path) => _openPage(path),
                onForm: (reply, text) => d.say(reply: reply, text: text),
              );
            }
          }
          // This month's payroll (P4b-3): its card, drawn from Payrolla now.
          if (m.kind == 'payroll_run') {
            final card = payrollCardOf(m.text);
            if (card != null) return PayrollChatCard(key: ValueKey('payroll-${x.id}'), auth: d.chats.auth, run: card.run, period: card.period);
          }
          // A payslip (P4b-4): the employee's own card, drawn from Payrolla now.
          if (m.kind == 'payslip') {
            final card = payslipCardOf(m.text);
            if (card != null) return PayslipChatCard(key: ValueKey('payslip-${x.id}'), auth: d.chats.auth, payslip: card.payslip, period: card.period);
          }
          if (m.kind == 'chart') {
            final chart = ChartData.parse(m.text);
            if (chart != null) return ChartCard(chart: chart);
          }
          // A payment's (or a batch's) line says how it stands now (H7).
          if (m.kind == 'diary' && (m.payment != null || m.batch != null)) {
            return PayDiaryLine(auth: d.chats.auth, words: m.text, when: 'You, ${clock(m.createdAt)}', payment: m.payment, batch: m.batch);
          }
          if (m.kind == 'diary') return DiaryLine(words: m.text, when: 'You, ${clock(m.createdAt)}');
          if (m.kind == 'receipt' || m.kind == 'paid_you') return PayChatCard(auth: d.chats.auth, kind: m.kind, text: m.text, payment: m.payment, status: m.paymentStatus);
          // startsRun: a run is one side's.
          final first = prev == null || prev.m.inbound != m.inbound;
          return Padding(
            key: x.run != null && !x.echo ? _runKeys.putIfAbsent(x.run!.id, GlobalKey.new) : null,
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
              onHold: () => _sel.hold(_idOf(x)),
            ),
          );
        },
      ),
    ));
  }
}

/// A message of the app's sign-up conversation (lib/screens/app_sign_up.dart)
/// in Taskiem's own bubble: the same name, words, time, ticks and buttons as
/// its conversation once the business is made.
class TaskiemSignupBubble extends StatelessWidget {
  const TaskiemSignupBubble({
    super.key,
    required this.id,
    required this.text,
    required this.mine,
    required this.first,
    required this.at,
    this.buttons = const [],
    this.pressable = false,
    this.onPress,
  });
  final String id;
  final String text;
  final bool mine;
  final bool first;
  final DateTime at;
  final List<({String id, String title})> buttons;
  final bool pressable;
  final void Function(String id, String title)? onPress;

  @override
  Widget build(BuildContext context) => _TaskiemBubble(
    x: TaskiemLine(TaskiemMessage(
      id: id,
      inbound: mine,
      channel: 'web',
      kind: 'text',
      text: text,
      createdAt: at,
      buttons: [for (final b in buttons) TaskiemMessageButtonsItem(id: b.id, title: b.title)],
    )),
    first: first,
    read: true,
    choice: null,
    pressable: pressable,
    onPress: (b) => onPress?.call(b.id ?? '', b.title),
    // The terms, and any page it names, open in the browser tab.
    onLink: (u) => openInBrowser(u),
    anyLink: true,
  );
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
    this.anyLink = false,
    this.known = const [],
    this.auth,
    this.now,
    this.onCustomer,
    this.onHold,
  });

  /// Held: selected (the screen's selection, menu.dart).
  final VoidCallback? onHold;

  /// Every web address in its words opens (signing up: the terms), not
  /// only a connect link or a canvas.
  final bool anyLink;

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
  final void Function(TaskiemMessageButtonsItem b) onPress;

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
    final buttons = m.buttons ?? const <TaskiemMessageButtonsItem>[];
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
      final links = widget.onLink == null
          ? const <({int start, int end, Uri uri})>[]
          : linksIn(text).where((l) => widget.anyLink || opensFromTaskiem(l.uri)).toList();
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
        // An invoice, a bill or a purchase on its own screen (the web's tool
        // sheet's row leads there too).
        final a = widget.auth;
        final open = ref.kind == 'customer'
            ? widget.onCustomer
            : a == null
            ? null
            : switch (ref.kind) {
                'order' => (String _) => openStorefrontOrder(context, a, ref.name),
                'invoice' => (String id) => openInvoice(context, a, id),
                'bill' => (String id) => openBill(context, a, id),
                'purchase' => (String id) => openPurchase(context, a, id),
                _ => null,
              };
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
        style: tkText(size: 15, lineHeight: 1.38, color: t.text),
      ),
    );
    // TaskiemChat.tsx: a message's menu holds Copy (not one still going).
    final copyable = !widget.x.local && m.text.isNotEmpty && widget.onHold != null;
    return BubbleHold(
      label: mine ? 'Your message' : 'Message from Taskiem',
      onHold: copyable ? (_) => widget.onHold!() : null,
      child: BubbleFrame(
        mine: mine,
        first: widget.first,
        stamp: stamp,
        inline: inline,
        children: [
          // As drawn: no name over Taskiem's words; this is its own
          // conversation (a screen reader hears "Message from Taskiem").
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
  final List<TaskiemMessageButtonsItem> buttons;
  final String? choice;
  final bool pressable;
  final void Function(TaskiemMessageButtonsItem b) onPress;

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
