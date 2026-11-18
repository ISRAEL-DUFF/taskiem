// You: a person's own side (docs/spec/taskiem-for-everyone.md §6, §7). A
// personal session opens here, on three tabs: **Chats** (the one list of
// chats with businesses and with people, docs/spec/people-chatting.md §7,
// with Requests and the archived ones apart; New chat finds people;
// **Start something**,
// invitations waiting, the switcher when the person is in businesses),
// **Orders** (every order and booking with any business, §13) and
// **Discover** (businesses to find, §10). Settings, in the head: name,
// phone (Change number, §5.5), saved addresses (§13), notifications and
// signing out. A business's chat link opens its card here (§8.2).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/applinks.dart';
import '../app/auth.dart';
import '../app/phone.dart';
import '../chat/approve.dart' show TkTextBox;
import '../app/notify.dart' show PhoneNotifications, PushNote, youChatOf;
import '../app/presence.dart';
import '../chat/people_cards.dart' show PayoutAccountPage;
import '../chat/people_chat.dart' show PeopleChatData, PeopleChatScreen;
import '../chat/people_contacts.dart' show ContactMatch, ContactsMatchScreen, DevicePhoneContacts, PeopleContactsScreen, PhoneContacts, contactsDay, recheckContacts;
import '../chat/people_find.dart';
import '../chat/people_live.dart';
import '../chat/people_search.dart';
import '../chat/people_standing.dart';
import '../chat/people_settings.dart' show BlockedPeopleScreen, PeoplePrivacyPage;
import '../chat/your_orders.dart' show YourOrderPage;
import '../app/prefs.dart' show Prefs;
import '../app/sound.dart' show chime;
import '../theme/look.dart';
import '../chat/addresses.dart' show SavedAddressesPage;
import '../chat/discover.dart' show DiscoverTab;
import '../chat/my_chat.dart';
import '../chat/your_orders.dart' show YourOrdersTab;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/load_more.dart';
import '../kit/polling.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkConfirm, showTkToast;
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'chat_link.dart';
import 'home_tabs.dart';
import 'second_proof.dart';
import 'start_something.dart';
import 'you_links.dart';
import 'you_account.dart';
import '../chat/composer.dart' show Pickers;
import '../app/route.dart' show youRouteOf;
import 'switcher.dart';

/// The words for an error, as the sign-in screens say them.
String _say(Object e) => switch (e) {
      ApiError() => e.message,
      Offline() => 'You’re offline. Try again when you’re back online.',
      _ => 'Something went wrong. Try again.',
    };

class YouHome extends StatefulWidget {
  const YouHome({super.key, required this.auth, this.links, this.now = DateTime.now, this.notify, this.prefs, this.contacts = const DevicePhoneContacts(), this.presence = true, this.live});
  final Auth auth;

  /// You's live connection (`GET /v1/me/live`): made here while [presence]
  /// is on, unless a test gives its own.
  final PeopleLive? live;

  /// The phone's contacts, read only once the person agrees (a test's own).
  final PhoneContacts contacts;

  /// Says the person is here while the app is open (people-chatting §13).
  final bool presence;

  /// Notifications on this phone, here You's: a business answering a chat.
  final PhoneNotifications? notify;
  final Prefs? prefs;

  /// Links that open the app: a second proof's link opened on this phone,
  /// a business's chat link.
  final LinkInbox? links;
  final DateTime Function() now;

  @override
  State<YouHome> createState() => _YouHomeState();
}

class _YouHomeState extends State<YouHome> with Polling {
  List<Invitation> _invitations = const [];
  String? _error;
  bool _busy = false;
  /// You's live connection: open while the app is in front (Presence opens
  /// and closes it); the screens on You read what it tells.
  late final PeopleLive? _live = widget.live ?? (widget.presence ? PeopleLive(widget.auth.transport) : null);
  late final MyChats _chats = MyChats(widget.auth, live: _live);
  late final Presence _presence = Presence(widget.auth.api, live: _live);

  /// The list read again every 10 seconds, or every 30 while the live
  /// connection is open (only to reconcile).
  final _slow = SlowWhileLive();

  /// Found by the daily re-check of the phone's contacts (§5.3): not in
  /// the person's contacts yet; kept only while You is open.
  List<ContactMatch> _fromContacts = const [];

  /// The person's standing with the moderators, read quietly: its notice
  /// shows while a decision is unseen.
  PersonStanding? _standing;

  /// Told each time Orders is shown again: it reads itself again.
  final _ordersShown = ValueNotifier<int>(0);

  /// The tabs, for a link that opens one.
  final _tabs = GlobalKey<HomeTabsState>();

  @override
  void initState() {
    super.initState();
    PeopleLive.hold(widget.auth, _live);
    _load();
    unawaited(_recheckContacts());
    if (widget.presence) {
      _presence.start();
    } else {
      _live?.start();
    }
    widget.links?.addListener(_linked);
    WidgetsBinding.instance.addPostFrameCallback((_) => _linked());
    // New messages show in the list while it is seen: not while the app
    // is put away (the server takes a read as the person being here) or
    // a screen covers it.
    startPolling(const Duration(seconds: 10), () {
      if (_slow.due(_live)) _chats.load();
    });
    // Notifications on this phone, on the person's own side: a tapped one
    // opens its chat; one that comes with the app open is a toast; asked
    // once.
    final notify = widget.notify;
    if (notify != null) {
      notify.addListener(_notified);
      notify.onArrived = _arrived;
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        await notify.start();
        if (!mounted) return;
        _notified();
        if (await notify.shouldAsk() && mounted) await _askOnce(notify);
      });
    }
  }

  /// A tapped notification: its chat (a business's or a person's), or
  /// Requests, over the list.
  void _notified() {
    final notify = widget.notify;
    if (notify == null || !mounted) return;
    final problem = notify.takeProblem();
    if (problem != null) showTkToast(context, problem, error: true);
    final link = notify.takePending();
    final id = youChatOf(link);
    if (id != null) {
      Navigator.of(context).popUntil((r) => r.isFirst);
      unawaited(_open(PersonChat.fromJson(_stub(id))));
      return;
    }
    final you = link == null ? null : youRouteOf(link);
    if (you == null) return;
    Navigator.of(context).popUntil((r) => r.isFirst);
    unawaited(openYouPage(context, widget.auth, you, open: _openId, openPerson: _openPerson, showTab: (i) => _tabs.currentState?.show(i), now: widget.now));
  }

  /// Enough of a chat to open it by its id (the rest is read as it opens).
  static Map<String, dynamic> _stub(String id) => {
    'id': id, 'business': '', 'name': 'Business', 'last': '', 'last_at': DateTime.now().toUtc().toIso8601String(), 'last_mine': false,
    'unread': 0, 'muted': false, 'archived': false, 'blocked': false, 'blocked_you': false, 'offers': 'unasked', 'ask_offers': false,
  };

  void _arrived(PushNote n) {
    if (!mounted) return;
    if (widget.prefs?.sound ?? true) unawaited(chime());
    final words = n.body.isEmpty ? n.title : '${n.title}: ${n.body}';
    showTkToast(context, words, action: 'Open', onAction: () => widget.notify?.open(n.data));
    unawaited(_chats.load());
  }

  Future<void> _askOnce(PhoneNotifications notify) async {
    final yes = await showTkConfirm(
      context,
      title: 'Get a notification when a business or a person messages you?',
      body: 'Taskiem tells you on this phone, even when the app is closed. You can change this in Settings.',
      ok: 'Yes',
      cancel: 'Not now',
    );
    if (yes) {
      await notify.turnOn();
      if (mounted && notify.error != null) showTkToast(context, notify.error!, error: true);
    } else {
      await notify.declined();
    }
  }

  @override
  void dispose() {
    final notify = widget.notify;
    notify?.removeListener(_notified);
    if (notify?.onArrived == _arrived) notify?.onArrived = null;
    if (widget.presence) _presence.stop();
    if (PeopleLive.of(widget.auth) == _live) PeopleLive.hold(widget.auth, null);
    if (widget.live == null) {
      _live?.dispose();
    } else {
      _live?.stop();
    }
    widget.links?.removeListener(_linked);
    _chats.dispose();
    _ordersShown.dispose();
    super.dispose();
  }

  /// Your standing, read quietly: nothing shown when it cannot be read.
  Future<void> _readStanding() async {
    try {
      final s = await widget.auth.api.getMyStanding();
      if (mounted) setState(() => _standing = s);
    } on Object {
      // No notice.
    }
  }

  Future<void> _openStanding() async {
    await tkPush<void>(context, YourStandingScreen(auth: widget.auth, now: widget.now));
    await _readStanding();
  }

  Future<void> _load() async {
    await Future.wait([
      _readStanding(),
      _chats.load(),
      () async {
        try {
          final r = await widget.auth.api.myInvitations();
          if (mounted) setState(() => _invitations = r.invitations);
        } on Object {
          // None shown; read again next time.
        }
      }(),
    ]);
  }

  /// A chat opened, and the list read again when it is closed.
  Future<void> _open(PersonChat c, {String? notice, String draft = ''}) => _openId(c.id, notice: notice, draft: draft);

  /// A row of the one list: a business's chat, or a person's.
  Future<void> _openItem(ChatListItem c) => c.kind == 'person' ? _openPerson(c.id) : _openId(c.id);

  /// A chat with a person opened by its id (or as just started); the list
  /// read again after.
  Future<void> _openPerson(String id, {PeopleChat? started}) async {
    await tkPush<void>(
      context,
      PeopleChatScreen(
        data: PeopleChatData(widget.auth, id, now: widget.now, started: started),
        notify: widget.notify,
        openBusiness: _openId,
        openOrder: (business, reference) => tkPush<void>(context, YourOrderPage.of(auth: widget.auth, business: business, reference: reference, onOpenChat: _openId, now: widget.now)),
      ),
    );
    await _chats.load();
  }

  /// The phone's contacts checked again, at most once a day, only if the
  /// person agreed before and the phone still allows it (§5.3).
  Future<void> _recheckContacts() async {
    final prefs = widget.prefs;
    if (prefs == null || !prefs.loaded) return;
    try {
      final found = await recheckContacts(api: widget.auth.api, contacts: widget.contacts, checked: prefs.contactsChecked, now: widget.now(), remember: prefs.setContactsChecked);
      if (found != null && mounted) setState(() => _fromContacts = found);
    } on Object {
      // Checked again another day.
    }
  }

  /// Agreed and checked from New chat: the day kept for the re-check.
  void _contactsChecked() => unawaited(widget.prefs?.setContactsChecked(contactsDay(widget.now())));

  /// Start a chat with someone found among the contacts.
  Future<void> _chatWith(String person) => chatWithPerson(context, widget.auth, _openPerson, person: person);

  /// A chat with a person opened at one of its messages (a search's hit, a
  /// starred message).
  Future<void> _openPersonAt(String id, String message) async {
    await tkPush<void>(
      context,
      PeopleChatScreen(
        data: PeopleChatData(widget.auth, id, now: widget.now, focus: message),
        notify: widget.notify,
        openBusiness: _openId,
        openOrder: (business, reference) => tkPush<void>(context, YourOrderPage.of(auth: widget.auth, business: business, reference: reference, onOpenChat: _openId, now: widget.now)),
      ),
    );
    await _chats.load();
  }

  /// New chat: finding people (people-chatting §5).
  Future<void> _newChat() async {
    await tkPush<void>(context, NewPeopleChatScreen(auth: widget.auth, openChat: _openPerson, contacts: widget.contacts, onContactsChecked: _contactsChecked));
    await _chats.load();
  }

  /// A chat opened by its id (from Orders and Discover too), with a tool
  /// over it when asked; the list read again when it is closed.
  Future<void> _openId(String id, {String? notice, String draft = '', String? tool}) async {
    await tkPush<void>(context, MyChatScreen(data: MyChatData(widget.auth, id, notice: notice, now: widget.now), draft: draft, notify: widget.notify, tool: tool));
    await _chats.load();
  }

  /// The email's link to confirm this phone (§5.4), or a business's chat
  /// link (§8.2), opened in the app.
  Future<void> _linked() async {
    final page = widget.links?.waiting == true ? widget.links!.take() : null;
    if (page == null || !mounted) return;
    // A person's link (people-chatting §5.2): who it is, then the chat.
    final person = personLinkOf(page);
    if (person != null) {
      Navigator.of(context).popUntil((r) => r.isFirst);
      await tkPush<void>(context, PersonLinkScreen(auth: widget.auth, code: person, openChat: _openPerson));
      await _chats.load();
      return;
    }
    final code = chatCodeOf(page);
    if (code != null) {
      // Seen on the way in: the chat starts at once.
      final auto = widget.links!.shownSignedOut;
      widget.links!.shownSignedOut = false;
      await tkPush<void>(
        context,
        ChatLinkScreen(
          auth: widget.auth,
          code: code,
          autoStart: auto,
          onStarted: (c) {
            Navigator.of(context).pop();
            _open(c, notice: c.notice);
          },
        ),
      );
      await _chats.load();
      return;
    }
    // You's own pages (/you, a chat, Orders, an order, Discover, a
    // business's profile): its tab, and the screen over it.
    final you = youRouteOf(page);
    if (you != null) {
      Navigator.of(context).popUntil((r) => r.isFirst);
      await openYouPage(context, widget.auth, you, open: _openId, openPerson: _openPerson, showTab: (i) => _tabs.currentState?.show(i), now: widget.now);
      return;
    }
    final token = Uri.parse(page).queryParameters['token'];
    if (!page.startsWith('/second-proof') || token == null) return;
    try {
      await widget.auth.api.secondProofEmailVerify(body: SecondProofEmailVerifyRequest(token: token));
      await widget.auth.refreshMe();
      if (mounted) showTkToast(context, 'Done: this device is yours now. Your businesses are open to you here.');
    } on Object catch (e) {
      if (mounted) showTkToast(context, _say(e), error: true);
    }
  }

  Future<void> _join(Invitation inv) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final j = await widget.auth.transport.send('POST', '/v1/me/invitations/${Uri.encodeComponent(inv.tenantId)}/accept');
      // Read as it came: the ways may name only those the person has.
      final r = (j as Map).cast<String, dynamic>();
      final token = r['token'] as String?;
      // Moved into the business: the app opens on it.
      if (token != null) return await widget.auth.signedInWith(token);
      await widget.auth.refreshMe();
      await _load();
      final need = r['sign_in'] as String?;
      if (!mounted) return;
      if (need != null && need != 'session_ended') {
        // A member now, but the business asks for a step first (its
        // sign-in rules, or a second proof on a new phone): that step,
        // then the business opens. The session stays on You till then.
        setState(() => _busy = false);
        await showBusinessStep(
          context,
          widget.auth,
          to: BusinessEntry(id: inv.tenantId, name: inv.tenant, locked: true, lock: need),
          need: need,
          ways: ProofWays.of(r['ways']),
        );
        return;
      }
      showTkToast(context, 'You joined ${inv.tenant}. Open it from Switch.');
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  /// A new phone's businesses stay locked until it is proved (§5.4).
  Future<void> _prove() => showSecondProof(
        context,
        widget.auth,
        ways: ProofWays.ofMe(widget.auth.me),
        onProved: () async {
          await widget.auth.refreshMe();
          if (mounted) showTkToast(context, 'Done: this device is yours now. Your businesses are open to you here.');
        },
      );

  Future<void> _decline(Invitation inv) async {
    setState(() => _busy = true);
    try {
      await widget.auth.api.declineInvitation(tenant: inv.tenantId);
      await _load();
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  /// You's tabs (§7): Chats, Orders and Discover; Settings is in the head.
  @override
  Widget build(BuildContext context) => HomeTabs(
    key: _tabs,
    count: () => _chats.unread,
    counted: _chats,
    offline: widget.auth.transport.offline,
    onShow: (i) {
      if (i == 1) _ordersShown.value++;
    },
    tabs: [
      (label: 'Chats', icon: 'b-chat', build: _chatsTab),
      (label: 'Orders', icon: 'b-box', build: (_) => YourOrdersTab(auth: widget.auth, onOpenChat: _openId, now: widget.now, shown: _ordersShown)),
      (label: 'Discover', icon: 'b-search', build: (_) => DiscoverTab(auth: widget.auth, onOpenChat: _openId)),
    ],
  );

  Widget _chatsTab(BuildContext context) {
    final auth = widget.auth;
    final t = TkTheme.tokens(context);
    final businesses = auth.me?.businesses ?? const <BusinessEntry>[];
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) => TkScreen(
        title: 'Chats',
        back: false,
        head: Row(
          children: [
            Text('Chats', style: tkDisplay(size: 30, letterSpacing: -1.1, color: t.text)),
            const SizedBox(width: 8),
            Text('You', style: tkText(size: 13, color: t.muted)),
          ],
        ),
        onRefresh: () async {
          await auth.refreshMe();
          await _load();
        },
        actions: [
          TkHeadButton(icon: 'b-plus', label: 'New chat', onTap: _newChat),
          const SizedBox(width: 6),
          if (businesses.isNotEmpty) ...[TkHeadButton(icon: 'loop', label: 'Switch', onTap: () => showSwitcher(context, auth)), const SizedBox(width: 6)],
          TkHeadButton(icon: 'settings', label: 'Settings', onTap: () => tkPush<void>(context, YouSettingsScreen(auth: auth, notify: widget.notify, onOpenChat: _openId, onOpenPerson: _openPerson, now: widget.now, prefs: widget.prefs))),
        ],
        children: [
          if (_invitations.isNotEmpty)
            TkSection(
              title: 'Invitations',
              count: _invitations.length,
              children: [
                TkPanel(
                  inset: 16,
                  children: [for (final inv in _invitations) _InvitationRow(inv: inv, busy: _busy, onJoin: () => _join(inv), onDecline: () => _decline(inv))],
                ),
              ],
            ),
          // Search across chats with people, and the starred messages.
          Semantics(
            button: true,
            label: 'Search chats with people',
            excludeSemantics: true,
            child: GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: () => tkPush<void>(context, PeopleSearchScreen(auth: auth, openAt: _openPersonAt, now: widget.now)),
              child: Container(
                constraints: const BoxConstraints(minHeight: 44),
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(999)),
                child: Row(
                  children: [
                    TkIcon('b-search', size: 18, stroke: 2, color: t.muted),
                    const SizedBox(width: 8),
                    Expanded(child: Text('Search chats with people', maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 15, color: t.muted))),
                  ],
                ),
              ),
            ),
          ),
          if (_error != null) TkErrorBox(_error!),
          if ((_standing?.unseen ?? 0) > 0) StandingNotice(standing: _standing!, onOpen: _openStanding),
          if (auth.me?.secondProof?.required == true)
            TkPanel(
              padding: const EdgeInsets.all(16),
              children: [
                Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Semantics(
                      liveRegion: true,
                      child: Text(
                        'You signed in on a new device, so your businesses are locked until you prove it\'s you.',
                        style: tkText(size: 14, color: t.text),
                      ),
                    ),
                    const SizedBox(height: 10),
                    TkButton(label: 'Prove it\'s you', onPressed: _prove),
                  ],
                ),
              ],
            ),
          ListenableBuilder(
            listenable: Listenable.merge([_chats, ?_live]),
            builder: (context, _) {
              final chats = _chats.chats;
              final archived = _chats.archived;
              final requests = _chats.requests;
              return Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_chats.error != null && chats == null) TkErrorBox(_chats.error!),
                  if (chats != null && chats.isEmpty && archived.isEmpty && requests == 0 && _fromContacts.isEmpty)
                    const TkPanel(
                      children: [
                        TkEmpty(
                          icon: 'chat',
                          title: 'No chats yet',
                          line: 'Chats with businesses and people will show here. Start one with New chat.',
                        ),
                      ],
                    ),
                  if (chats != null && (chats.isNotEmpty || archived.isNotEmpty || requests > 0 || _fromContacts.isNotEmpty))
                    TkPanel(
                      inset: 76,
                      children: [
                        if (requests > 0)
                          TkListRow(
                            title: 'Message requests',
                            sub: 'From people you haven’t chatted with',
                            icon: 'b-people',
                            amount: '$requests',
                            semanticsLabel: 'Message requests, $requests',
                            onTap: () async {
                              await tkPush<void>(context, PeopleRequestsScreen(auth: auth, openChat: _openPerson, now: widget.now));
                              await _chats.load();
                            },
                          ),
                        if (_fromContacts.isNotEmpty)
                          TkListRow(
                            title: _fromContacts.length == 1 ? '1 of your contacts is on Taskiem' : '${_fromContacts.length} of your contacts are on Taskiem',
                            sub: 'Found from your phone’s contacts today',
                            icon: 'b-people',
                            onTap: () async {
                              await tkPush<void>(context, ContactsMatchScreen(auth: auth, contacts: widget.contacts, onChat: _chatWith, found: _fromContacts));
                              if (mounted) setState(() => _fromContacts = const []);
                            },
                          ),
                        if (archived.isNotEmpty)
                          TkListRow(
                            title: 'Archived',
                            icon: 'archive',
                            amount: '${archived.length}${_chats.moreArchived ? '+' : ''}',
                            semanticsLabel: 'Archived chats, ${archived.length}${_chats.moreArchived ? ' or more' : ''}',
                            onTap: () => tkPush<void>(context, ArchivedChatsScreen(data: _chats, now: widget.now, onOpen: _openItem)),
                          ),
                        MyChatRows(chats: chats, now: widget.now(), onOpen: _openItem, transport: auth.transport, live: _live),
                      ],
                    ),
                  // The next page as the list is scrolled to its end.
                  if (chats != null && _chats.more) TkLoadMore(label: 'Show more chats', onMore: _chats.loadMore),
                ],
              );
            },
          ),
          TkButton(
            label: 'Start something',
            primary: true,
            minHeight: 44,
            onPressed: () => tkPush<void>(context, StartSomethingScreen(auth: auth)),
          ),
          Text('A business, or workflows to automate your work.', textAlign: TextAlign.center, style: tkHint(t)),
          // The web's Your businesses: the switcher's rows, on You.
          if (businesses.isNotEmpty)
            TkSection(
              title: 'Your businesses',
              children: [
                TkPanel(
                  children: [
                    for (final b in businesses)
                      TkListRow(
                        title: b.name,
                        sub: businessLine(b),
                        icon: b.kind == 'workflows' ? 'workflows' : (b.locked ? 'lock' : null),
                        letters: b.name,
                        semanticsLabel: '${b.name}, ${b.kind == 'workflows' ? 'workflows' : 'business'}${b.locked ? ', ${lockWords(b.lock)}' : ''}',
                        onTap: () => showSwitcher(context, auth, open: b),
                      ),
                  ],
                ),
              ],
            ),
        ],
      ),
    );
  }
}

class _InvitationRow extends StatelessWidget {
  const _InvitationRow({required this.inv, required this.busy, required this.onJoin, required this.onDecline});
  final Invitation inv;
  final bool busy;
  final VoidCallback onJoin;
  final VoidCallback onDecline;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final roles = inv.roles.isEmpty ? '' : ' as ${inv.roles.join(', ')}';
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(inv.tenant, style: tkText(size: 15, weight: 600, color: t.text)),
          Text('Invited you$roles', style: tkHint(t)),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(child: TkButton(label: 'Join', primary: true, enabled: !busy, onPressed: onJoin)),
              const SizedBox(width: 8),
              Expanded(child: TkButton(label: 'Decline', enabled: !busy, onPressed: onDecline)),
            ],
          ),
        ],
      ),
    );
  }
}

/// Settings on **You** (§7): who the person is (their photo, only theirs),
/// their phone with Change number, saved addresses, notifications, privacy,
/// signing out and deleting the account.
class YouSettingsScreen extends StatelessWidget {
  const YouSettingsScreen({super.key, required this.auth, this.notify, this.onOpenChat, this.onOpenPerson, this.pickers = const Pickers(), this.now = DateTime.now, this.prefs});
  final Auth auth;

  /// What this phone keeps (the daily contacts re-check's day).
  final Prefs? prefs;

  /// Opens a chat with a person (Your contacts' Chat); else it is pushed.
  final OpenPeopleChat? onOpenPerson;

  /// Notifications on this phone, for You.
  final PhoneNotifications? notify;

  /// Opens a chat (Privacy's Ask to erase); else the chat is pushed here.
  final OpenMyChat? onOpenChat;
  final Pickers pickers;
  final DateTime Function() now;

  Future<void> _openChat(BuildContext context, String id, {String? notice, String draft = '', String? tool}) =>
      tkPush<void>(context, MyChatScreen(data: MyChatData(auth, id, notice: notice, now: now), draft: draft, notify: notify, tool: tool));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) {
        final user = auth.me?.user;
        final phone = user?.phone as String?;
        final email = user?.emailPlaceholder == true ? null : user?.email;
        Widget line(String label, String value) => Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [Text(label, style: tkHint(t)), Text(value, style: tkText(size: 15, weight: 500, color: t.text))],
              ),
            );
        final look = TkLook.of(context);
        return TkScreen(
          title: 'Settings',
          children: [
            TkPanel(
              inset: 16,
              children: [
                Padding(padding: const EdgeInsets.all(16), child: MyPhotoCard(auth: auth, pickers: pickers)),
                line('Name', user?.name ?? ''),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 12, 12),
                  child: Row(
                    children: [
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [Text('Phone', style: tkHint(t)), Text(phone == null ? 'No phone yet: add one to sign in with a code.' : showPhone(phone), style: tkText(size: 15, weight: 500, color: t.text))],
                        ),
                      ),
                      TkButton(
                        label: phone == null ? 'Add a phone' : 'Change number',
                        onPressed: () => tkPush<void>(context, ChangeNumberScreen(auth: auth)),
                      ),
                    ],
                  ),
                ),
                if (email != null) line('Email', email),
              ],
            ),
            // Saved addresses (§13): kept on your side, offered when ordering.
            TkSection(
              title: 'Ordering',
              inset: 56,
              children: [
                TkListRow(
                  title: 'Saved addresses',
                  sub: 'Where your orders are delivered',
                  icon: 'b-pin',
                  square: TkLook.of(context).orange,
                  onTap: () => tkPush<void>(context, SavedAddressesPage(auth: auth)),
                ),
              ],
            ),
            TkSection(
              title: 'You and businesses',
              inset: 56,
              children: [
                TkListRow(
                  title: 'Notifications',
                  sub: 'What businesses may tell you, and this phone',
                  icon: 'b-bell',
                  square: look.orange,
                  onTap: () => tkPush<void>(context, YouNotificationsPage(auth: auth, notify: notify)),
                ),
                TkListRow(
                  title: 'Privacy',
                  sub: 'What businesses see, and having it erased',
                  icon: 'b-lock',
                  square: look.blue,
                  onTap: () => tkPush<void>(
                    context,
                    YouPrivacyPage(auth: auth, onOpenChat: onOpenChat ?? (id, {notice, draft = '', tool}) => _openChat(context, id, notice: notice, draft: draft, tool: tool), now: now),
                  ),
                ),
              ],
            ),
            // Chats with people (people-chatting §5.4, §8.1, §11).
            TkSection(
              title: 'You and people',
              inset: 56,
              children: [
                TkListRow(
                  title: 'Privacy with people',
                  sub: 'Who can find you, message you and see your last seen',
                  icon: 'b-shield',
                  square: look.teal,
                  onTap: () => tkPush<void>(context, PeoplePrivacyPage(auth: auth, prefs: prefs)),
                ),
                TkListRow(
                  title: 'Your contacts',
                  sub: 'People you added',
                  icon: 'b-people',
                  square: look.blue,
                  onTap: () => tkPush<void>(
                    context,
                    PeopleContactsScreen(
                      auth: auth,
                      onChat: (person) => chatWithPerson(
                        context,
                        auth,
                        onOpenPerson ?? (id, {started}) => tkPush<void>(context, PeopleChatScreen(data: PeopleChatData(auth, id, now: now, started: started), notify: notify)),
                        person: person,
                      ),
                    ),
                  ),
                ),
                TkListRow(
                  title: 'Your link and QR code',
                  sub: 'So people can chat with you without your number',
                  icon: 'b-qr',
                  square: look.violet,
                  onTap: () => tkPush<void>(context, MyPersonLinkScreen(auth: auth)),
                ),
                TkListRow(
                  title: 'Blocked people',
                  icon: 'b-x-ring',
                  square: look.red,
                  onTap: () => tkPush<void>(context, BlockedPeopleScreen(auth: auth, now: now)),
                ),
                TkListRow(
                  title: 'Your standing',
                  sub: 'What Taskiem’s moderators decided, and how to appeal',
                  icon: 'b-alert',
                  square: look.orange,
                  onTap: () => tkPush<void>(context, YourStandingScreen(auth: auth, now: now)),
                ),
                TkListRow(
                  title: 'Your bank account for shares',
                  sub: 'Where friends pay you back. Taskiem moves no money',
                  icon: 'b-bank',
                  square: look.lime,
                  onTap: () => tkPush<void>(context, PayoutAccountPage(auth: auth)),
                ),
              ],
            ),
            TkButton(label: 'Sign out', minHeight: 44, onPressed: auth.signOut),
            TkSection(
              inset: 56,
              children: [
                TkListRow(title: 'Delete your account', icon: 'b-x-ring', square: look.red, onTap: () => tkPush<void>(context, DeleteAccountScreen(auth: auth))),
              ],
            ),
          ],
        );
      },
    );
  }
}

/// Settings > Phone > Change number (§5.5), as the web's (ChangeNumber.tsx):
/// the new number and a code to it; where the server says this phone must
/// also prove it is the person's, it asks first whether they still have the
/// old number: **Yes** sends it a code too, **No** proves it another way.
class ChangeNumberScreen extends StatefulWidget {
  const ChangeNumberScreen({super.key, required this.auth});
  final Auth auth;

  @override
  State<ChangeNumberScreen> createState() => _ChangeNumberScreenState();
}

enum _Step { number, old, code }

class _ChangeNumberScreenState extends State<ChangeNumberScreen> {
  final _phone = TextEditingController();
  final _code = TextEditingController();
  final _old = TextEditingController();
  _Step _step = _Step.number;
  String _e164 = '';
  PhoneChangeStartResponse? _sent;
  bool _needsOld = false;
  bool _busy = false;
  bool _done = false;
  String? _error;
  String? _note;
  int _left = 0;
  Timer? _tick;

  @override
  void dispose() {
    _tick?.cancel();
    _phone.dispose();
    _code.dispose();
    _old.dispose();
    super.dispose();
  }

  void _wait() {
    _tick?.cancel();
    _left = resendAfter;
    _tick = Timer.periodic(const Duration(seconds: 1), (t) {
      if (!mounted) return t.cancel();
      setState(() => _left--);
      if (_left <= 0) t.cancel();
    });
  }

  Future<void> _run(Future<void> Function() f) async {
    if (_busy || _done) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on ApiError catch (e) {
      _error = codeProblem(e);
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<PhoneChangeStartResponse> _start(String phone, {bool confirmOld = false, bool sms = false}) async {
    final r = await widget.auth.api.phoneChangeStart(body: PhoneChangeStartRequest(phone: phone, confirmOld: confirmOld ? true : null, via: sms ? 'sms' : null));
    _sent = r;
    _code.clear();
    _old.clear();
    _wait();
    return r;
  }

  void _send() {
    final phone = toE164(_phone.text);
    if (phone == null) return setState(() => _error = notAPhone);
    _note = null;
    _run(() async {
      final r = await _start(phone);
      _e164 = phone;
      _needsOld = r.needsOldCode;
      _step = r.needsOldCode ? _Step.old : _Step.code;
    });
  }

  /// Yes, the old number: a code to it too.
  void _yes() => _run(() async {
        await _start(_e164, confirmOld: true);
        _step = _Step.code;
      });

  /// No: another proof (a passkey, the email's link, the password, the
  /// chat PIN); then only the new number's code.
  Future<void> _no() => showSecondProof(
        context,
        widget.auth,
        ways: ProofWays.ofMe(widget.auth.me),
        onProved: () async {
          await widget.auth.refreshMe();
          if (mounted) {
            setState(() {
              _needsOld = false;
              _step = _Step.code;
            });
          }
        },
      );

  void _verify() {
    if (_code.text.length != 6 || (_needsOld && _old.text.length != 6)) return;
    _run(() async {
      try {
        final r = await widget.auth.api.phoneChangeVerify(
          body: PhoneChangeVerifyRequest(code: _code.text, oldCode: _needsOld ? _old.text : null, phone: _e164),
        );
        _done = true;
        await widget.auth.refreshMe();
        if (!mounted) return;
        showTkToast(context, 'Your number is now ${showPhone(r.phone)}');
        unawaited(Navigator.of(context).maybePop());
      } on ApiError catch (e) {
        // Run out: a new code, from the number.
        if (!e.has('ticket_expired')) rethrow;
        _note = codeExpired;
        _step = _Step.number;
        _sent = null;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final sent = _sent;
    final old = widget.auth.me?.user?.phone as String?;
    final List<Widget> body = switch (_step) {
      _Step.number => [
          if (_note != null) ...[Semantics(liveRegion: true, child: Text(_note!, style: tkText(size: 14, color: t.text))), const SizedBox(height: 10)],
          TkKitField(
            label: 'New phone number',
            hint: 'A Nigerian number can start with 0. For another country, start with + and its code.',
            child: TkTextBox(key: const ValueKey('new-phone'), controller: _phone, phone: true),
          ),
          const SizedBox(height: 14),
          TkButton(label: _busy ? 'Sending…' : 'Send a code', primary: true, minHeight: 44, enabled: !_busy, onPressed: _send),
        ],
      _Step.old => [
          Text(
            'To keep your account safe, we also check it\'s you. Do you still have your old number${old == null ? '' : ', ${showPhone(old)}'}?',
            style: tkText(size: 15, color: t.text),
          ),
          const SizedBox(height: 14),
          TkButton(label: 'Yes, send it a code', primary: true, minHeight: 44, enabled: !_busy, onPressed: _yes),
          const SizedBox(height: 8),
          TkButton(label: 'No, prove it\'s me another way', minHeight: 44, enabled: !_busy, onPressed: _no),
        ],
      _Step.code => [
          Semantics(
            liveRegion: true,
            child: Text(
              '${sentWords(sent?.sentVia ?? 'whatsapp', _e164)}${_needsOld && old != null && sent?.oldSentVia != null ? ' And one to your old number, ${showPhone(old)}.' : ''}',
              style: tkText(size: 14, color: t.text),
            ),
          ),
          const SizedBox(height: 14),
          TkKitField(
            label: 'Code sent to your new number',
            child: TkTextBox(key: const ValueKey('new-code'), controller: _code, digits: 6),
          ),
          if (_left > 0)
            Padding(padding: const EdgeInsets.only(top: 6), child: Text('You can ask for a new code in ${countdown(_left)}.', style: tkHint(t)))
          else
            TkLinkButton(label: 'Send a new code', onPressed: _busy ? null : () => _run(() => _start(_e164, confirmOld: _needsOld))),
          if (sent?.sentVia != 'sms')
            TkLinkButton(label: 'Send by SMS instead', onPressed: _busy ? null : () => _run(() => _start(_e164, confirmOld: _needsOld, sms: true))),
          if (_needsOld) ...[
            const SizedBox(height: 14),
            TkKitField(label: 'Code sent to your old number', child: TkTextBox(key: const ValueKey('old-code'), controller: _old, digits: 6)),
          ],
          const SizedBox(height: 14),
          TkButton(label: _busy || _done ? 'Checking…' : 'Confirm the new number', primary: true, minHeight: 44, enabled: !_busy && !_done, onPressed: _verify),
        ],
    };
    return TkScreen(
      title: old == null ? 'Add your phone' : 'Change your number',
      children: [
        TkPanel(
          padding: const EdgeInsets.all(16),
          children: [
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Your account keeps everything: your chats, orders and businesses.', style: tkHint(t)),
                const SizedBox(height: 14),
                ...body,
              ],
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
