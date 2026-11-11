// The chats: the web's chat list (web/src/pages/Links.tsx ChatPage, its list
// pane; ChatRow.tsx), drawn the same at a phone's width: the head, the
// search, the chips, Taskiem's conversation, the businesses asking to link,
// and the conversations with linked businesses.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'customer.dart';
import 'explore.dart';
import 'live.dart';
import 'words.dart';

/// What the list shows, loaded as the web's page loads it.
class ChatsData extends ChangeNotifier {
  ChatsData(this.auth, {DateTime Function()? now, this.live}) : now = now ?? DateTime.now {
    // taskiemSeen.ts: a first visit begins at now.
    taskiemSeen = this.now();
    _liveSub = live?.events.listen((e) {
      // A message, one gone, a conversation's settings: the list again,
      // once for a burst (ChatLive.tsx reloadSoon).
      if (e is LiveTaskiem) {
        // taskiemSeen.ts addTaskiemUnread: what Taskiem says after the
        // newest seen, while the person is not looking at it.
        final seen = taskiemSeen;
        if (seen != null && !viewingTaskiem) {
          final said = e.messages.where((m) => m['inbound'] != true && DateTime.parse(m['created_at'] as String).isAfter(seen)).length;
          if (said > 0) {
            taskiemUnread += said;
            notifyListeners();
          }
        }
      }
      if (e is LiveMessage || e is LiveGone || e is LiveConversation) {
        _soon?.cancel();
        _soon = Timer(const Duration(milliseconds: 400), load);
      }
    });
    live?.addListener(notifyListeners);
  }

  final Auth auth;
  final DateTime Function() now;

  /// The chat as it happens, when followed.
  final ChatLive? live;
  StreamSubscription<LiveEvent>? _liveSub;
  Timer? _soon;
  Timer? _every;

  /// When the person last saw Taskiem's conversation at its newest end
  /// (taskiemSeen.ts): what Taskiem said after it is unread.
  DateTime? taskiemSeen;

  /// What Taskiem said since, as the list's row counts it.
  int taskiemUnread = 0;

  /// The newest seen.
  void markTaskiemSeen(DateTime at) {
    if (taskiemSeen != null && !at.isAfter(taskiemSeen!)) return;
    taskiemSeen = at;
    if (taskiemUnread != 0) {
      taskiemUnread = 0;
      notifyListeners();
    }
  }

  /// Whether a conversation's other side is typing: from the stream while
  /// it is connected, else as the list was last told.
  bool typingIn(LinkConversation c) => live?.connected == true ? live!.typing.contains(c.link) : c.typing;

  /// Asks for the list again every 30 seconds while shown (Links.tsx
  /// useLiveRefresh), besides what the stream tells.
  void follow(bool on) {
    _every?.cancel();
    _every = on ? Timer.periodic(const Duration(seconds: 30), (_) => load()) : null;
  }

  @override
  void dispose() {
    _liveSub?.cancel();
    _soon?.cancel();
    _every?.cancel();
    live?.removeListener(notifyListeners);
    super.dispose();
  }

  List<LinkConversation> conversations = const [];
  List<BusinessLink> links = const [];
  int waiting = 0;
  int archivedCustomers = 0;
  bool chatTools = false;
  bool loaded = false;
  String? error;
  final Map<String, Uint8List?> logos = {};

  /// Taskiem's conversation is open and in front.
  bool viewingTaskiem = false;

  /// The conversations' photos, voice notes and PDFs, fetched as shown.
  late final ChatFiles files = ChatFiles(auth.transport);

  TaskiemApi get api => auth.api;

  bool _area(String key) => auth.me?.areas?[key] != false;
  bool get linksOn => _area('links');

  /// Customers who wrote to the business's own WhatsApp number: for those
  /// who handle orders, with Orders on (Links.tsx customersOn).
  bool get customersOn => auth.can('storefront.manage|order.handle') && _area('orders');
  List<CustomerChat> customers = const [];
  List<CustomerChat> archivedCustomerChats = const [];

  /// Requests to link we sent to businesses not yet on Taskiem.
  List<BlindLinkRequest> sent = const [];

  /// Whether other businesses may find this one, and whether it was asked.
  LinkSettings? linkSettings;

  /// Links.tsx: nothing linked, asked, waiting or sent.
  bool get noLinks => links.where((l) => const ['asked_us', 'active', 'requested', 'not_accepted'].contains(l.state)).isEmpty && sent.isEmpty;

  /// The person may answer requests to link (link.manage, as the web's `can`).
  bool get manage => auth.can('link.manage') && linksOn;

  Future<void> load() async {
    error = null;
    await Future.wait([
      _try(() async {
        conversations = linksOn ? (await api.listLinkChats()).conversations : const [];
      }),
      _try(() async {
        final r = linksOn ? await api.listLinks() : null;
        links = r?.links ?? const [];
        sent = r?.sent ?? const [];
      }),
      _try(() async {
        chatTools = (await api.getChatTools()).chatTools;
        waiting = chatTools ? (await api.getChatAlerts()).alerts.length : 0;
      }),
      _try(() async {
        final archived = customersOn ? (await api.listCustomerChats(archived: '1')).chats : const <CustomerChat>[];
        archivedCustomers = archived.length;
        archivedCustomerChats = archived;
      }),
      _try(() async {
        customers = customersOn ? (await api.listCustomerChats()).chats : const [];
      }),
      _try(() async {
        linkSettings = manage ? await api.getLinkSettings() : null;
      }),
    ]);
    loaded = true;
    notifyListeners();
  }

  Future<void> _try(Future<void> Function() f) async {
    try {
      await f();
    } on ApiError catch (e) {
      error ??= e.message;
    } on Offline {
      error ??= 'You’re offline. Your chats show when you’re back online.';
    }
  }

  /// A business's logo, fetched once with the session.
  Uint8List? logo(String business) {
    if (logos.containsKey(business)) return logos[business];
    logos[business] = null;
    auth.transport.bytes('/v1/links/$business/logo').then((b) {
      logos[business] = Uint8List.fromList(b);
      notifyListeners();
    }, onError: (_) {});
    return null;
  }

  /// Links.tsx answer: a request to link accepted, declined or blocked
  /// (POST /v1/links/{id}/{answer}); the list read again. Null when done,
  /// else why not.
  Future<String?> answer(BusinessLink l, String a) async {
    try {
      await auth.transport.send('POST', '/v1/links/${Uri.encodeComponent(l.id)}/$a', body: const {});
    } on ApiError catch (e) {
      return e.message;
    } on Offline {
      return 'You’re offline. Try again when you’re back online.';
    }
    await load();
    return null;
  }

  /// Links.tsx: the requests to answer.
  List<BusinessLink> get asked => links.where((l) => l.state == 'asked_us').toList();

  /// The messages this person starred (the Starred chip), newest first;
  /// null until read.
  List<LinkMessage>? starred;

  /// Read again each time the Starred chip is chosen (a star may have
  /// changed since).
  Future<void> loadStarred() async {
    starred = null;
    notifyListeners();
    try {
      starred = (await api.starredLinkMessages()).messages;
    } on ApiError catch (e) {
      starred = const [];
      error = e.message;
    } on Offline {
      starred = const [];
      error = 'You’re offline. Your chats show when you’re back online.';
    }
    notifyListeners();
  }

  /// The name of a linked business's conversation ("They" when unknown).
  String nameOf(String link) => conversations.where((c) => c.link == link).firstOrNull?.name ?? 'They';

  /// links.ts groups: the businesses we asked, waiting (or not accepted).
  List<BusinessLink> get waitingLinks => links.where((l) => l.state == 'requested' || l.state == 'not_accepted').toList();
  int get unreadCount => conversations.where((c) => !c.archived && c.unread > 0).length;
  int get archivedCount => conversations.where((c) => c.archived).length + archivedCustomers;
}

enum ChatFilter { all, unread, starred, archived }

class ChatsScreen extends StatefulWidget {
  const ChatsScreen({super.key, required this.data, this.onMenu, this.onFind, this.onOpen, this.onOpenTaskiem, this.onOpenCustomer, this.onOpenAt});

  final ChatsData data;
  final VoidCallback? onMenu;
  final VoidCallback? onFind;

  /// A conversation with a linked business, opened.
  final ValueChanged<LinkConversation>? onOpen;

  /// Taskiem's conversation, opened.
  final VoidCallback? onOpenTaskiem;

  /// A customer's conversation, opened.
  final ValueChanged<CustomerChat>? onOpenCustomer;

  /// A linked business's conversation, opened at one of its messages (a
  /// starred message, a search's hit).
  final void Function(LinkConversation c, String message)? onOpenAt;

  @override
  State<ChatsScreen> createState() => _ChatsScreenState();
}

class _ChatsScreenState extends State<ChatsScreen> {
  ChatFilter _filter = ChatFilter.all;
  bool _answering = false;

  // The search (Links.tsx): from the first letter the list narrows to what
  // matches; from two, other businesses, messages and customers are
  // looked for too.
  final _q = TextEditingController();
  Timer? _searchSoon;
  SearchLinksResponse? _found;
  List<LinkMessage> _foundMessages = const [];
  SearchCustomerChatsResponse? _foundCustomers;
  Object? _searchError;
  String get _query => _q.text.trim();

  @override
  void dispose() {
    _searchSoon?.cancel();
    _q.dispose();
    super.dispose();
  }

  void _typed() {
    setState(() {});
    _searchSoon?.cancel();
    final q = _query;
    if (q.length < 2) {
      _found = null;
      _foundMessages = const [];
      _foundCustomers = null;
      return;
    }
    _searchSoon = Timer(const Duration(milliseconds: 150), () => _search(q));
  }

  Future<void> _search(String q) async {
    final d = widget.data;
    _found = null;
    if (d.linksOn) {
      d.api.searchLinks(q: q).then((r) {
        if (mounted && _query == q) setState(() => (_found = r, _searchError = null));
      }, onError: (Object e) {
        if (mounted && _query == q) setState(() => _searchError = e);
      });
      d.api.searchLinkChats(q: q).then((r) {
        if (mounted && _query == q) setState(() => _foundMessages = r.messages);
      }, onError: (_) {});
    }
    if (d.customersOn) {
      d.api.searchCustomerChats(q: q).then((r) {
        if (mounted && _query == q) setState(() => _foundCustomers = r);
      }, onError: (_) {});
    }
  }

  /// RequestSheet: how we trade with them, then Send request.
  Future<void> _ask({required String title, required String hint, required Map<String, dynamic> to, required String done}) async {
    final sent = await showTkModal<bool>(
      context,
      title: title,
      builder: (context) => _RequestForm(data: widget.data, hint: hint, to: to),
    );
    if (sent != true || !mounted) return;
    showTkToast(context, done);
    _q.clear();
    await widget.data.load();
  }

  Future<void> _cancel(BusinessLink pending) async {
    final why = await widget.data.answer(pending, 'cancel');
    if (!mounted) return;
    showTkToast(context, why ?? 'Request withdrawn', error: why != null);
    if (_query.length >= 2) _search(_query);
  }

  Future<void> _answer(BusinessLink l, String a, String done) async {
    if (_answering) return;
    _answering = true;
    final why = await widget.data.answer(l, a);
    _answering = false;
    if (!mounted) return;
    showTkToast(context, why ?? done, error: why != null);
  }

  @override
  void initState() {
    super.initState();
    _q.addListener(_typed);
    if (!widget.data.loaded) widget.data.load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final top = MediaQuery.paddingOf(context).top;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ColoredBox(
        color: t.panel,
        child: ListenableBuilder(
          listenable: widget.data,
          builder: (context, _) {
            final d = widget.data;
            final archived = _filter == ChatFilter.archived;
            final query = _query;
            final searching = d.linksOn && query.length >= 2;
            final narrowed = d.linksOn && query.length == 1 ? matchChats(d.conversations, query) : d.conversations;
            final listed = narrowed.where((c) => c.archived == archived && (_filter != ChatFilter.unread || c.unread > 0)).toList();
            final customers = (archived ? d.archivedCustomerChats : d.customers)
                .where((c) => (_filter != ChatFilter.unread || c.unread > 0) && (query.isEmpty || c.name.toLowerCase().contains(query.toLowerCase())))
                .toList();
            return Stack(
              children: [
                ListView(
                  padding: EdgeInsets.only(top: top, bottom: 88 + MediaQuery.paddingOf(context).bottom),
                  children: [
                    _Head(name: d.auth.me?.tenantName, onMenu: widget.onMenu),
                    if (d.linksOn || d.customers.isNotEmpty || d.archivedCustomers > 0) _Search(controller: _q),
                    if (!searching && d.loaded && (d.conversations.isNotEmpty || d.customers.isNotEmpty || d.archivedCustomers > 0))
                      _Chips(
                        filter: _filter,
                        unread: d.unreadCount,
                        archived: d.archivedCount,
                        onPick: (f) {
                          setState(() => _filter = f);
                          if (f == ChatFilter.starred) d.loadStarred();
                        },
                      ),
                    if (d.error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: TkErrorBox(d.error!)),
                    if (_searchError != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: TkErrorBox(_searchError is ApiError ? (_searchError as ApiError).message : 'You’re offline. Try again when you’re back online.')),
                    if (!searching && !archived && _filter != ChatFilter.starred && (query.isEmpty || 'taskiem'.startsWith(query.toLowerCase())))
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: ChatRow(
                          accent: true,
                          title: 'Taskiem',
                          unread: d.taskiemUnread,
                          onTap: widget.onOpenTaskiem,
                          preview: d.waiting > 0
                              ? Text(
                                  d.waiting == 1 ? '1 thing needs you' : '${d.waiting} things need you',
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: tkText(size: 14.5, weight: 700, color: t.tintText),
                                )
                              : _previewText(context, 'Your own conversation with Taskiem, as on your phone'),
                        ),
                      ),
                    if (d.customersOn && _filter != ChatFilter.starred)
                      query.length >= 2
                          ? _CustomersFound(found: _foundCustomers, data: d, onOpen: widget.onOpenCustomer)
                          : _Customers(chats: customers, archived: archived, now: d.now(), onOpen: widget.onOpenCustomer),
                    if (searching)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: _Results(
                          data: d,
                          query: query,
                          found: _found,
                          messages: _foundMessages,
                          // Under the search box (its 16 px margin collapsing
                          // into the heading's 32), or under customers found.
                          first: (_foundCustomers?.chats.isEmpty ?? true) && (_foundCustomers?.messages.isEmpty ?? true),
                          onOpen: widget.onOpen,
                          onOpenAt: widget.onOpenAt,
                          onAsk: (c) => _ask(
                            title: 'Ask ${c.name} to link',
                            hint: 'Linked, your invoices and orders reach each other by themselves. Nothing is shared until they accept.',
                            to: {'business': c.business},
                            done: 'Asked ${c.name} to link',
                          ),
                          onBlind: (b) => _ask(
                            title: 'Ask ${b.shown} to link',
                            hint: '${blindRowLine(b.kind)} You hear only if it accepts.',
                            to: b.kind == 'phone' ? {'phone': query} : {'address': b.shown},
                            done: 'Request sent',
                          ),
                          onCancel: _cancel,
                        ),
                      )
                    else if (d.linksOn && d.loaded && d.noLinks && _filter != ChatFilter.starred)
                      const _NoLinks()
                    else if (d.asked.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: _Asked(links: d.asked, manage: d.manage, now: d.now(), onAnswer: _answer),
                      ),
                    if (!searching && _filter == ChatFilter.starred && !d.noLinks)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: _Starred(data: d, onOpenAt: widget.onOpenAt, afterCard: d.asked.isNotEmpty),
                      ),
                    if (!searching && _filter != ChatFilter.starred && d.conversations.isNotEmpty && !d.noLinks)
                      Padding(
                        padding: EdgeInsets.only(left: 16, right: 16, top: d.asked.isEmpty ? 0 : 6),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            if (listed.isEmpty)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 8),
                                child: Text(
                                  query.length == 1
                                      ? 'No chat matches “$query”.'
                                      : archived
                                      ? 'No archived chats.'
                                      : _filter == ChatFilter.unread
                                      ? 'Nothing unread.'
                                      : 'No chats.',
                                  style: tkHint(t),
                                ),
                              ),
                            for (final c in listed)
                              ChatRow(
                                title: c.name,
                                logo: c.logo ? d.logo(c.business) : null,
                                letter: c.name,
                                time: c.last == null ? null : listTime(c.last!.createdAt, d.now()),
                                ticks: c.last != null && c.last!.mine && c.last!.kind != 'card' ? (read: c.last!.readAt != null) : null,
                                preview: _previewText(
                                  context,
                                  d.typingIn(c)
                                      ? 'typing…'
                                      : c.last != null
                                      ? listPreview(c.last!, c.name)
                                      : c.state == 'active'
                                      ? 'Linked'
                                      : 'The link has ended',
                                  typing: d.typingIn(c),
                                ),
                                unread: c.unread,
                                pinned: c.pinned,
                                muted: c.muted,
                                onTap: widget.onOpen == null ? null : () => widget.onOpen!(c),
                              ),
                          ],
                        ),
                      ),
                    if (!searching && d.linksOn && !d.noLinks && (d.waitingLinks.isNotEmpty || d.sent.isNotEmpty))
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: _Waiting(links: d.waitingLinks, sent: d.sent, now: d.now()),
                      ),
                    if (!searching && d.manage && d.linkSettings != null && d.linkSettings!.askedAt == null && _filter != ChatFilter.starred) _Findable(data: d),
                  ],
                ),
                if (d.linksOn)
                  Positioned(
                    right: 16,
                    bottom: 16 + MediaQuery.paddingOf(context).bottom,
                    child: _FindButton(
                      // Find a business: Explore, over the chats.
                      onTap: widget.onFind ?? () => showExplore(context, chats: d, onOpen: (c) => widget.onOpen?.call(c)),
                    ),
                  ),
              ],
            );
          },
        ),
      ),
    );
  }

  static Widget _previewText(BuildContext context, String s, {bool typing = false}) {
    final t = TkTheme.tokens(context);
    return Text(
      s,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      softWrap: false,
      style: tkText(size: 14.5, color: typing ? t.ok : t.muted),
    );
  }
}

/// `.chat-list-head`: Chats, the business's name, and the menu.
class _Head extends StatelessWidget {
  const _Head({this.name, this.onMenu});
  final String? name;
  final VoidCallback? onMenu;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
      child: Row(
        children: [
          Expanded(
            child: Semantics(
              header: true,
              child: Text('Chats', style: tkText(size: 26, weight: 800, lineHeight: 1.35, letterSpacing: -0.52, color: t.text)),
            ),
          ),
          if (name != null) ...[const SizedBox(width: 6), Text(name!, style: tkText(size: 13, color: t.muted))],
          const SizedBox(width: 6),
          Semantics(
            button: true,
            label: 'More about the chats',
            child: TkPress(
              onTap: onMenu ?? () {},
              // The web's `more`, turned upright, its dots bolder (`.chat-menu-wrap svg`).
              child: SizedBox(
                width: 40,
                height: 40,
                child: Center(
                  child: RotatedBox(quarterTurns: 1, child: TkIcon('more', size: 18, stroke: 3, color: t.text)),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// `.filters .search`: the round search box.
class _Search extends StatelessWidget {
  const _Search({required this.controller});
  final TextEditingController controller;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Semantics(
        container: true,
        label: 'Find a business',
        child: Container(
          height: 42,
          decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(999)),
          padding: const EdgeInsets.only(left: 11, right: 11),
          child: Row(
            children: [
              TkIcon('search', size: 16, color: t.muted),
              const SizedBox(width: 7),
              Expanded(
                child: Material(
                  type: MaterialType.transparency,
                  child: TextField(
                    controller: controller,
                    textInputAction: TextInputAction.search,
                    cursorColor: t.text,
                    showCursor: !TkField.debugHideCaret,
                    style: tkText(size: 15, weight: 500, color: t.text),
                    decoration: InputDecoration.collapsed(
                      hintText: 'Search chats, messages, businesses',
                      hintStyle: tkText(size: 15, weight: 500, color: cssMix(t.muted, const Color(0x00000000), 0.72)),
                    ),
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

/// `.chat-chips`: All, Unread, Starred, Archived; sliding sideways.
class _Chips extends StatelessWidget {
  const _Chips({required this.filter, required this.unread, required this.archived, required this.onPick});
  final ChatFilter filter;
  final int unread;
  final int archived;
  final ValueChanged<ChatFilter> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final chips = [
      (ChatFilter.all, 'All'),
      (ChatFilter.unread, unread > 0 ? 'Unread ($unread)' : 'Unread'),
      (ChatFilter.starred, 'Starred'),
      (ChatFilter.archived, 'Archived ($archived)'),
    ];
    // Within the list's 16 px margins, clipped there (`.chat-chips`).
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: SizedBox(
        height: 50,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(vertical: 8),
          itemCount: chips.length,
          separatorBuilder: (_, _) => const SizedBox(width: 8),
          itemBuilder: (context, i) {
            final (f, label) = chips[i];
            final on = filter == f;
            return Semantics(
              button: true,
              selected: on,
              child: TkPress(
                onTap: () => onPick(f),
                child: Container(
                  height: 34,
                  padding: const EdgeInsets.symmetric(horizontal: 14),
                  alignment: Alignment.center,
                  decoration: BoxDecoration(
                    border: Border.all(color: t.borderStrong),
                    borderRadius: BorderRadius.circular(999),
                    boxShadow: outsideOnly(t.shadow),
                  ),
                  child: Text(
                    label,
                    style: tkText(size: 14, weight: on ? 600 : 500, lineHeight: 1.2, color: on ? t.tintText : t.text),
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

/// ChatRow.tsx: one conversation, as WhatsApp's row.
class ChatRow extends StatelessWidget {
  const ChatRow({
    super.key,
    required this.title,
    required this.preview,
    this.letter = '',
    this.logo,
    this.time,
    this.ticks,
    this.unread = 0,
    this.pinned = false,
    this.muted = false,
    this.accent = false,
    this.onTap,
  });

  final String title;
  final Widget preview;
  final String letter;
  final Uint8List? logo;
  final String? time;
  final ({bool read})? ticks;
  final int unread;
  final bool pinned;
  final bool muted;
  final bool accent;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Row(
          children: [
            accent ? const _AccentAvatar() : ChatAvatar(name: letter, logo: logo, size: 52),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          softWrap: false,
                          style: tkText(size: 16, weight: 600, color: t.text),
                        ),
                      ),
                      if (time != null) ...[
                        const SizedBox(width: 8),
                        Text(
                          time!,
                          style: tkText(size: 12, weight: unread > 0 ? 600 : 400, color: unread > 0 ? t.ok : t.muted),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      if (ticks != null) ...[
                        TkIcon('tick-double', size: 16, stroke: 2, color: ticks!.read ? t.chatTickRead : t.muted),
                        const SizedBox(width: 4),
                      ],
                      Expanded(child: preview),
                      if (pinned) ...[const SizedBox(width: 8), TkIcon('pin', size: 15, color: t.muted)],
                      if (muted) ...[const SizedBox(width: 6), TkIcon('mute', size: 15, color: t.muted)],
                      if (unread > 0) ...[
                        const SizedBox(width: 8),
                        Semantics(label: '$unread unread ${unread == 1 ? 'message' : 'messages'}', child: UnreadCount(unread)),
                      ],
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Taskiem's avatar: the spark, lime on pine.
class _AccentAvatar extends StatelessWidget {
  const _AccentAvatar();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      width: 52,
      height: 52,
      decoration: BoxDecoration(color: t.brand, shape: BoxShape.circle),
      alignment: Alignment.center,
      child: TkTaskiemMark(size: 20, color: t.brandDark),
    );
  }
}

/// ChatAvatar.tsx: the logo, else the initials on the name's tone.
class ChatAvatar extends StatelessWidget {
  const ChatAvatar({super.key, required this.name, this.logo, this.size = 52, this.letterSize});
  final String name;
  final Uint8List? logo;
  final double size;

  /// The initials' size: the row's 20 px at 52, the head's 15 px.
  final double? letterSize;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final l = logo;
    if (l != null) {
      return ClipOval(
        child: Image.memory(l, width: size, height: size, fit: BoxFit.cover, gaplessPlayback: true),
      );
    }
    // The web's ChatAvatar sets a tone by the name (`data-tone`), but
    // chat.css's `.chat-avatar` comes after kit.css's tones and wins: in
    // the chat every initials avatar is pine with lime letters.
    final (bg, ink) = (t.brand, t.brandDark);
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: bg, shape: BoxShape.circle),
      alignment: Alignment.center,
      child: Text(
        initialsOf(name),
        style: tkText(size: letterSize ?? size * 20 / 52, weight: 800, letterSpacing: 0.2, color: ink),
      ),
    );
  }
}

/// kit.css `[data-tone]`: a tone's ground and ink.
(Color, Color) toneColours(TkTokens t, String tone) => switch (tone) {
  'pine' => (t.tonePineBg, t.tonePineInk),
  'lime' => (t.toneLimeBg, t.toneLimeInk),
  'peach' => (t.tonePeachBg, t.tonePeachInk),
  'yellow' => (t.toneYellowBg, t.toneYellowInk),
  'green' => (t.toneGreenBg, t.toneGreenInk),
  'lavender' => (t.toneLavenderBg, t.toneLavenderInk),
  'blue' => (t.toneBlueBg, t.toneBlueInk),
  _ => (t.toneSandBg, t.toneSandInk),
};

/// Links.tsx: the businesses asking to link, each with its answers.
/// One row of a section of links (ui.tsx Row): a tinted letter or icon,
/// the name and its state's badge, a line under them, and its buttons.
typedef _LinkRow = ({String? letter, String? icon, Tone? tone, String title, String? badge, Tone? badgeTone, String sub, Widget? actions});

/// Links.tsx's sections of links (Asks to link, Waiting for an answer): a
/// heading, then the rows in a card.
class _LinkSection extends StatelessWidget {
  const _LinkSection({this.title, required this.rows, this.top = 32});
  final String? title;
  final List<_LinkRow> rows;

  /// The heading's room above (CSS's 32, less a margin it collapses into).
  final double top;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Color ink(Tone? tone) => switch (tone) {
      Tone.warn => t.warn,
      Tone.info => t.info,
      Tone.ok => t.ok,
      _ => t.muted,
    };
    // `.list-icon`, `.list-icon.warn`, `.list-icon.info`.
    (Color, Color) tinted(Tone? tone) => switch (tone) {
      Tone.warn => (t.tintWarn, t.tintWarnText),
      Tone.info => (cssMix(t.info, t.panel, 0.12), cssMix(t.info, t.text, 0.85)),
      _ => (t.tint, t.tintText),
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title != null) _Heading(title!, top: top),
        Container(
          margin: const EdgeInsets.only(bottom: 16),
          decoration: BoxDecoration(
            color: t.panel,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(20),
          ),
          child: Column(
            children: [
              for (final r in rows)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Container(
                        width: 40,
                        height: 40,
                        decoration: BoxDecoration(color: tinted(r.tone).$1, borderRadius: BorderRadius.circular(12)),
                        alignment: Alignment.center,
                        child: r.icon != null
                            ? TkIcon(r.icon!, size: 20, color: tinted(r.tone).$2)
                            : Text(
                                r.letter!.isEmpty ? '' : String.fromCharCode(r.letter!.runes.first).toUpperCase(),
                                style: tkText(size: 16, weight: 700, color: tinted(r.tone).$2),
                              ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            SizedBox(
                              // `.item-top`: as tall as the badge, else the name's line.
                              height: r.badge != null ? 25 : 23.25,
                              child: Row(
                                children: [
                                  Expanded(
                                    child: Text(
                                      r.title,
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: tkText(size: 15, weight: 600, color: t.text),
                                    ),
                                  ),
                                  if (r.badge != null) ...[const SizedBox(width: 12), TkBadge(r.badge!, tone: ink(r.badgeTone))],
                                ],
                              ),
                            ),
                            SizedBox(height: r.badge != null ? 3 : 2),
                            Text(r.sub, style: tkHint(t)),
                            if (r.actions != null) ...[const SizedBox(height: 3), r.actions!],
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}

/// Links.tsx's Asks to link: who asks, and Accept, Decline or Block for
/// those who may answer (an owner or admin answers it, for the others).
class _Asked extends StatelessWidget {
  const _Asked({required this.links, required this.manage, required this.now, this.onAnswer});
  final List<BusinessLink> links;
  final bool manage;
  final DateTime now;

  /// A request answered: the answer, and what is said once it is done.
  final void Function(BusinessLink l, String answer, String done)? onAnswer;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final badge = stateWords('asked_us')!;
    return _LinkSection(
      title: 'Asks to link',
      rows: [
        for (final l in links)
          (
            letter: l.name,
            icon: null,
            tone: Tone.warn,
            title: l.name,
            badge: badge.label,
            badgeTone: badge.tone,
            sub: '${roleLine(l.role)} · asked ${whenWords(l.requestedAt, now)}',
            actions: manage
                ? Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Row(
                      children: [
                        TkSmallButton(
                          label: 'Accept',
                          semantics: 'Accept ${l.name}',
                          primary: true,
                          onPressed: onAnswer == null ? null : () => onAnswer!(l, 'accept', 'Linked with ${l.name}'),
                        ),
                        const SizedBox(width: 8),
                        TkSmallButton(
                          label: 'Decline',
                          semantics: 'Decline ${l.name}',
                          onPressed: onAnswer == null ? null : () => onAnswer!(l, 'decline', 'Declined. They are not told.'),
                        ),
                        const SizedBox(width: 8),
                        TkSmallButton(
                          label: 'Block',
                          semantics: 'Block ${l.name}',
                          danger: true,
                          onPressed: onAnswer == null
                              ? null
                              : () async {
                                  final yes = await showTkConfirm(
                                    context,
                                    title: 'Block ${l.name}?',
                                    body: 'Their requests will not reach you, and they are not told.',
                                    ok: 'Block',
                                    danger: true,
                                  );
                                  if (yes) onAnswer!(l, 'block', 'Blocked');
                                },
                        ),
                      ],
                    ),
                  )
                : Text('An owner or admin answers this.', style: tkHint(t)),
          ),
      ],
    );
  }
}

/// Links.tsx's Starred messages: each by its business, opened at it.
class _Starred extends StatelessWidget {
  const _Starred({required this.data, this.onOpenAt, this.afterCard = false});
  final ChatsData data;

  /// Under the Asks to link card: its 16 px margin below collapses into
  /// the heading's 32 above (CSS margins).
  final bool afterCard;
  final void Function(LinkConversation c, String message)? onOpenAt;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = data.starred;
    return Semantics(
      container: true,
      label: 'Starred messages',
      explicitChildNodes: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _Heading('Starred messages', top: afterCard ? 16 : 32),
          if (list == null)
            const SizedBox(height: 120)
          else if (list.isEmpty)
            Text('Nothing starred yet. Open a message’s menu and choose Star to keep it here.', style: tkHint(t))
          else
            for (final m in list)
              ChatRow(
                title: data.nameOf(m.link),
                letter: data.nameOf(m.link),
                time: listTime(m.createdAt, data.now()),
                preview: Text(
                  previewLine(m, data.nameOf(m.link)),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  softWrap: false,
                  style: tkText(size: 14.5, color: t.muted),
                ),
                onTap: () {
                  final c = data.conversations.where((c) => c.link == m.link).firstOrNull;
                  if (c != null) onOpenAt?.call(c, m.id);
                },
              ),
        ],
      ),
    );
  }
}

/// The list's `h2`: 15 px semibold, 32 px above (less a margin it
/// collapses into), 12 below.
class _Heading extends StatelessWidget {
  const _Heading(this.text, {this.top = 32});
  final String text;
  final double top;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: EdgeInsets.only(top: top, bottom: 12),
      child: Semantics(
        header: true,
        child: Text(text, style: tkText(size: 15, weight: 600, lineHeight: 1.35, letterSpacing: -0.15, color: t.text)),
      ),
    );
  }
}

/// Links.tsx's search results: the linked businesses found (by name, or a
/// word of their last message), the messages found, and the businesses on
/// Taskiem to ask, or the request a phone number or a page address sends.
class _Results extends StatelessWidget {
  const _Results({
    required this.data,
    required this.query,
    required this.found,
    required this.messages,
    required this.first,
    required this.onAsk,
    required this.onBlind,
    required this.onCancel,
    this.onOpen,
    this.onOpenAt,
  });
  final ChatsData data;
  final String query;
  final SearchLinksResponse? found;
  final List<LinkMessage> messages;
  final bool first;
  final ValueChanged<LinkCard> onAsk;
  final ValueChanged<SearchLinksResponseBlind> onBlind;
  final ValueChanged<BusinessLink> onCancel;
  final ValueChanged<LinkConversation>? onOpen;
  final void Function(LinkConversation c, String message)? onOpenAt;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final d = data;
    final hits = matchChats(d.conversations, query);
    final chats = [
      for (final l in d.links)
        if (matchLinked([l], query).isNotEmpty || hits.any((c) => c.business == l.business))
          if (d.conversations.where((c) => c.business == l.business).firstOrNull case final c?) (l, c),
    ];
    final blind = found?.blind;
    final others = (found?.results ?? const <LinkCard>[]).where((c) => !d.conversations.any((l) => l.business == c.business)).toList();
    var top = first ? 16.0 : 32.0;
    double next() {
      final v = top;
      top = 32;
      return v;
    }

    Widget preview(String s) => Text(s, maxLines: 1, overflow: TextOverflow.ellipsis, softWrap: false, style: tkText(size: 14.5, color: t.muted));
    return Semantics(
      container: true,
      label: 'Search results',
      explicitChildNodes: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (chats.isNotEmpty) ...[
            _Heading('Chats', top: next()),
            for (final (l, c) in chats)
              ChatRow(
                title: c.name,
                letter: c.name,
                logo: c.logo ? d.logo(c.business) : null,
                preview: preview(roleLine(l.role)),
                unread: c.unread,
                onTap: onOpen == null ? null : () => onOpen!(c),
              ),
          ],
          if (messages.isNotEmpty) ...[
            _Heading('Messages', top: next()),
            for (final m in messages)
              ChatRow(
                title: d.nameOf(m.link),
                letter: d.nameOf(m.link),
                time: listTime(m.createdAt, d.now()),
                preview: preview(previewLine(m, d.nameOf(m.link))),
                onTap: () {
                  final c = d.conversations.where((c) => c.link == m.link).firstOrNull;
                  if (c != null) onOpenAt?.call(c, m.id);
                },
              ),
          ],
          if (blind != null)
            _LinkSection(
              title: 'Businesses on Taskiem',
              top: next(),
              rows: [
                (
                  letter: null,
                  icon: 'chat',
                  tone: null,
                  title: 'Send a link request to ${blind.shown}',
                  badge: null,
                  badgeTone: null,
                  sub: blindRowLine(blind.kind),
                  actions: d.manage
                      ? Padding(
                          padding: const EdgeInsets.only(top: 8),
                          child: Row(children: [TkSmallButton(label: 'Send request', onPressed: () => onBlind(blind))]),
                        )
                      : null,
                ),
              ],
            )
          else if (found == null) ...[
            _Heading('Businesses on Taskiem', top: next()),
            Semantics(label: 'Searching businesses…', child: const SizedBox(height: 96)),
          ] else if (others.isEmpty) ...[
            _Heading('Businesses on Taskiem', top: next()),
            _Empty(icon: 'search', words: 'No business found for “$query”. Only businesses that chose to be found are listed.'),
          ] else
            _LinkSection(
              title: 'Businesses on Taskiem',
              top: next(),
              rows: [
                for (final c in others)
                  () {
                    final state = stateWords(c.state);
                    final action = cardAction(c);
                    final pending = requestTo(d.links, c.business);
                    final Widget? actions = action == 'request'
                        ? (d.manage
                              ? Padding(
                                  padding: const EdgeInsets.only(top: 8),
                                  child: Row(
                                    children: [TkSmallButton(label: 'Request link', semantics: 'Request link with ${c.name}', onPressed: () => onAsk(c))],
                                  ),
                                )
                              : Text('An owner or admin can link with this business.', style: tkHint(t)))
                        : c.state == 'requested' && d.manage && pending != null
                        ? Padding(
                            padding: const EdgeInsets.only(top: 8),
                            child: Row(
                              children: [TkSmallButton(label: 'Cancel request', semantics: 'Cancel request to ${c.name}', onPressed: () => onCancel(pending))],
                            ),
                          )
                        : action == 'answer'
                        ? Text('They asked you: answer under Asks to link.', style: tkHint(t))
                        : null;
                    return (
                      letter: c.name,
                      icon: null,
                      tone: null,
                      title: c.name,
                      badge: state?.label,
                      badgeTone: state?.tone,
                      sub: foundSub(c),
                      actions: actions,
                    );
                  }(),
              ],
            ),
        ],
      ),
    );
  }
}

/// ui.tsx EmptyState in a card: an icon on its tint, then the words.
class _Empty extends StatelessWidget {
  const _Empty({required this.icon, required this.words});
  final String icon;
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 56),
      decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(20)),
      child: Column(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(color: t.tint, borderRadius: BorderRadius.circular(16)),
            alignment: Alignment.center,
            child: TkIcon(icon, size: 26, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand),
          ),
          const SizedBox(height: 14),
          Text(words, textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
        ],
      ),
    );
  }
}

/// CustomerChat.tsx CustomerSearch: the customers found by name or number,
/// and the messages that hold the words.
class _CustomersFound extends StatelessWidget {
  const _CustomersFound({required this.found, required this.data, this.onOpen});
  final SearchCustomerChatsResponse? found;
  final ChatsData data;
  final ValueChanged<CustomerChat>? onOpen;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final f = found;
    if (f == null || (f.chats.isEmpty && f.messages.isEmpty)) return const SizedBox.shrink();
    final now = data.now();
    Widget preview(String s) => Text(s, maxLines: 1, overflow: TextOverflow.ellipsis, softWrap: false, style: tkText(size: 14.5, color: t.muted));
    CustomerChat? chatOf(String id) => [...data.customers, ...data.archivedCustomerChats, ...f.chats].where((c) => c.id == id).firstOrNull;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Semantics(
        container: true,
        label: 'Customers found',
        explicitChildNodes: true,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (f.chats.isNotEmpty) ...[
              const _Heading('Customers', top: 16),
              for (final c in f.chats)
                ChatRow(
                  title: customerTitle(c),
                  letter: customerLetter(c),
                  time: listTime(c.lastAt, now),
                  preview: preview(c.last),
                  unread: c.unread,
                  onTap: onOpen == null ? null : () => onOpen!(c),
                ),
            ],
            if (f.messages.isNotEmpty) ...[
              _Heading('In customers’ messages', top: f.chats.isEmpty ? 16 : 32),
              for (final m in f.messages)
                ChatRow(
                  title: RegExp(r'^\+\d').hasMatch(m.name) ? phoneWords(m.name) : m.name,
                  letter: m.name,
                  time: listTime(m.createdAt, now),
                  preview: preview('${m.mine ? 'You: ' : ''}${m.body}'),
                  onTap: () {
                    final c = chatOf(m.chatId);
                    if (c != null) onOpen?.call(c);
                  },
                ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Links.tsx RequestSheet's form: what the request means, how we trade
/// with them, Send request or Not now; closes with true once sent.
class _RequestForm extends StatefulWidget {
  const _RequestForm({required this.data, required this.hint, required this.to});
  final ChatsData data;
  final String hint;
  final Map<String, dynamic> to;

  @override
  State<_RequestForm> createState() => _RequestFormState();
}

class _RequestFormState extends State<_RequestForm> {
  String _role = 'supplier';
  bool _busy = false;
  String? _error;

  Future<void> _send() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.data.auth.transport.send('POST', '/v1/links', body: {...widget.to, 'role': _role});
      if (mounted) Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      setState(() => _error = e.message);
    } on Offline {
      setState(() => _error = 'You’re offline. Try again when you’re back online.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(widget.hint, style: tkHint(t)),
        const SizedBox(height: 12),
        Semantics(
          container: true,
          label: 'How do you trade with them?',
          explicitChildNodes: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('How do you trade with them?', style: tkText(size: 13, weight: 600, color: t.text)),
              const SizedBox(height: 6),
              for (final (role, label) in roleChoices)
                Semantics(
                  inMutuallyExclusiveGroup: true,
                  checked: _role == role,
                  label: label,
                  excludeSemantics: true,
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: () => setState(() => _role = role),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 6),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Container(
                            width: 16,
                            height: 16,
                            decoration: BoxDecoration(
                              shape: BoxShape.circle,
                              border: Border.all(color: _role == role ? t.primary : t.borderStrong, width: _role == role ? 5 : 1),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Text(label, style: tkText(size: 14, color: t.text)),
                        ],
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
        if (_error != null) ...[const SizedBox(height: 8), TkErrorBox(_error!)],
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkPlainButton(label: 'Send request', primary: true, onTap: _busy ? null : _send),
            TkPlainButton(label: 'Not now', onTap: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

/// Links.tsx's Waiting for an answer: the businesses asked, and the
/// requests sent to those not yet on Taskiem.
class _Waiting extends StatelessWidget {
  const _Waiting({required this.links, required this.sent, required this.now});
  final List<BusinessLink> links;
  final List<BlindLinkRequest> sent;
  final DateTime now;

  @override
  Widget build(BuildContext context) {
    final sentWords = stateWords('sent')!;
    return _LinkSection(
      title: 'Waiting for an answer',
      rows: [
        for (final l in links)
          (
            letter: l.name,
            icon: null,
            tone: Tone.info,
            title: l.name,
            badge: stateWords(l.state)!.label,
            badgeTone: stateWords(l.state)!.tone,
            sub: 'Asked ${whenWords(l.requestedAt, now)}',
            actions: null,
          ),
        for (final b in sent)
          (
            letter: null,
            icon: 'chat',
            tone: Tone.info,
            title: b.shown,
            badge: sentWords.label,
            badgeTone: sentWords.tone,
            sub: 'Sent ${whenWords(b.createdAt, now)}',
            actions: null,
          ),
      ],
    );
  }
}

/// `.new-chat`: Find a business, over the list's corner.
class _FindButton extends StatelessWidget {
  const _FindButton({this.onTap});
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    // Lime on pine; in dark, pine on lime.
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : t.brandDark;
    return Semantics(
      button: true,
      label: 'Find a business to link with',
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap ?? () {},
        child: Container(
          height: 56,
          padding: const EdgeInsets.symmetric(horizontal: 20),
          decoration: BoxDecoration(
            color: t.primary,
            borderRadius: BorderRadius.circular(16),
            boxShadow: [BoxShadow(offset: const Offset(0, 4), blurRadius: 9.53, color: t.chatShadow)],
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              TkIcon('plus', size: 20, color: ink),
              const SizedBox(width: 8),
              Text('Find a business', style: tkText(size: 15, weight: 600, lineHeight: 1.2, color: ink)),
            ],
          ),
        ),
      ),
    );
  }
}

/// `.unread-count`: how many, on the ok colour.
class UnreadCount extends StatelessWidget {
  const UnreadCount(this.count, {super.key});
  final int count;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      constraints: const BoxConstraints(minWidth: 20),
      padding: const EdgeInsets.symmetric(horizontal: 7),
      decoration: BoxDecoration(color: t.ok, borderRadius: BorderRadius.circular(999)),
      alignment: Alignment.center,
      child: Text('$count', style: tkText(size: 12, weight: 600, color: t.panel)),
    );
  }
}

/// CustomerChat.tsx CustomerRows: the Customers section, how many wait.
class _Customers extends StatelessWidget {
  const _Customers({required this.chats, required this.archived, required this.now, this.onOpen});
  final List<CustomerChat> chats;
  final bool archived;
  final DateTime now;
  final ValueChanged<CustomerChat>? onOpen;

  @override
  Widget build(BuildContext context) {
    if (chats.isEmpty) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final waiting = chats.where((c) => c.unread > 0).length;
    final head = archived ? 'Archived customers' : 'Customers';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            header: true,
            child: Text(
              '$head${waiting > 0 && !archived ? ' · ${waiting == 1 ? '1 waiting' : '$waiting waiting'}' : ''}',
              style: tkText(size: 15, weight: 600, lineHeight: 1.35, letterSpacing: -0.15, color: t.text),
            ),
          ),
          const SizedBox(height: 6),
          for (final c in chats)
            ChatRow(
              title: customerTitle(c),
              letter: customerLetter(c),
              time: listTime(c.lastAt, now),
              preview: _ChatsScreenState._previewText(context, c.stopped ? 'Sent STOP' : '${c.lastMine ? 'You: ' : ''}${c.last}'),
              unread: c.unread,
              onTap: onOpen == null ? null : () => onOpen!(c),
            ),
        ],
      ),
    );
  }
}

/// ui.tsx EmptyState (`.empty-state`): no linked businesses yet.
class _NoLinks extends StatelessWidget {
  const _NoLinks();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 56),
      decoration: BoxDecoration(
        color: t.panel,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(20),
      ),
      child: Column(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(color: t.tint, borderRadius: BorderRadius.circular(16)),
            alignment: Alignment.center,
            child: TkIcon('chat', size: 26, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand),
          ),
          const SizedBox(height: 14),
          Text(
            'No linked businesses yet. Search for a supplier or a customer that is on Taskiem, and ask to link.',
            textAlign: TextAlign.center,
            style: tkText(size: 14, color: t.muted),
          ),
        ],
      ),
    );
  }
}

/// Links.tsx FindableQuestion: may other businesses find this one?
class _Findable extends StatefulWidget {
  const _Findable({required this.data});
  final ChatsData data;

  @override
  State<_Findable> createState() => _FindableState();
}

class _FindableState extends State<_Findable> {
  bool _busy = false;

  Future<void> _answer(bool? findable) async {
    setState(() => _busy = true);
    try {
      await widget.data.auth.transport.send('PUT', '/v1/link-settings', body: {'findable': findable});
      await widget.data.load();
    } on Object {
      // Asked again next time.
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final s = widget.data.linkSettings!;
    Widget button(String label, bool primary, VoidCallback onTap) => Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: TkPress(
        onTap: _busy ? null : onTap,
        // As wide as its words, as a button is.
        child: IntrinsicWidth(
          child: Container(
            constraints: const BoxConstraints(minHeight: 36),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: primary ? t.primary : t.panel,
              border: Border.all(color: primary ? t.primary : t.borderStrong),
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(label, style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: primary ? t.primaryText : t.text)),
          ),
        ),
      ),
    );
    return Semantics(
      container: true,
      child: Container(
        // `margin: 14px 16px 16px`; after the empty card's 16 px below, the
        // two collapse into 16 (CSS block margins).
        margin: EdgeInsets.fromLTRB(16, widget.data.noLinks ? 0 : 14, 16, 16),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: t.panel,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(20),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Semantics(
              header: true,
              child: Text(
                'Let other businesses on Taskiem find you?',
                style: tkText(size: 15, weight: 600, lineHeight: 1.35, letterSpacing: -0.15, color: t.text),
              ),
            ),
            const SizedBox(height: 12),
            Text.rich(
              TextSpan(
                children: [
                  const TextSpan(text: 'They would find you by your name, what you sell and your area, from what your storefront already shows, or from your '),
                  TextSpan(
                    text: 'business profile',
                    style: TextStyle(color: dark ? t.brandDark : t.brand, decoration: TextDecoration.underline),
                  ),
                  TextSpan(text: '. ${findableLine(s)}'),
                ],
              ),
              style: tkText(size: 13, color: t.muted),
            ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [button('Yes, let them find us', true, () => _answer(true)), button('Not now', false, () => _answer(null))],
            ),
          ],
        ),
      ),
    );
  }
}

/// links.ts findableLine: what "Let other businesses find us" says.
String findableLine(LinkSettings s) {
  if (s.findable == null) {
    return s.findableNow
        ? 'On, because your storefront is published: businesses find you by your name, what you sell and your area.'
        : 'Off until you publish your storefront or turn it on.';
  }
  return s.findable == true
      ? 'On: businesses find you by your name, what you sell and your area.'
      : 'Off: only someone who has your phone number or page address can ask.';
}
