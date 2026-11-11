// The chats: the web's chat list (web/src/pages/Links.tsx ChatPage, its list
// pane; ChatRow.tsx), drawn the same at a phone's width: the head, the
// search, the chips, Taskiem's conversation, the businesses asking to link,
// and the conversations with linked businesses.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'words.dart';

/// What the list shows, loaded as the web's page loads it.
class ChatsData extends ChangeNotifier {
  ChatsData(this.auth, {DateTime Function()? now}) : now = now ?? DateTime.now;

  final Auth auth;
  final DateTime Function() now;

  List<LinkConversation> conversations = const [];
  List<BusinessLink> links = const [];
  int waiting = 0;
  int archivedCustomers = 0;
  bool chatTools = false;
  bool loaded = false;
  String? error;
  final Map<String, Uint8List?> logos = {};

  TaskiemApi get api => auth.api;

  bool _area(String key) => auth.me?.areas?[key] != false;
  bool get linksOn => _area('links');

  /// The person may answer requests to link (link.manage, as the web's `can`).
  bool get manage => (auth.me?.permissions ?? const []).contains('link.manage') && linksOn;

  Future<void> load() async {
    error = null;
    await Future.wait([
      _try(() async {
        conversations = linksOn ? (await api.listLinkChats()).conversations : const [];
      }),
      _try(() async {
        links = linksOn ? (await api.listLinks()).links : const [];
      }),
      _try(() async {
        chatTools = (await api.getChatTools()).chatTools;
        waiting = chatTools ? (await api.getChatAlerts()).alerts.length : 0;
      }),
      _try(() async {
        archivedCustomers = (await api.listCustomerChats(archived: '1')).chats.length;
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

  /// Links.tsx: the requests to answer.
  List<BusinessLink> get asked => links.where((l) => l.state == 'asked_us').toList();
  int get unreadCount => conversations.where((c) => !c.archived && c.unread > 0).length;
  int get archivedCount => conversations.where((c) => c.archived).length + archivedCustomers;
}

enum ChatFilter { all, unread, starred, archived }

class ChatsScreen extends StatefulWidget {
  const ChatsScreen({super.key, required this.data, this.onMenu, this.onFind});

  final ChatsData data;
  final VoidCallback? onMenu;
  final VoidCallback? onFind;

  @override
  State<ChatsScreen> createState() => _ChatsScreenState();
}

class _ChatsScreenState extends State<ChatsScreen> {
  ChatFilter _filter = ChatFilter.all;

  @override
  void initState() {
    super.initState();
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
            final listed = d.conversations.where((c) => c.archived == archived && (_filter != ChatFilter.unread || c.unread > 0)).toList();
            return Stack(
              children: [
                ListView(
                  padding: EdgeInsets.only(top: top, bottom: 88 + MediaQuery.paddingOf(context).bottom),
                  children: [
                    _Head(name: d.auth.me?.tenantName, onMenu: widget.onMenu),
                    const _Search(),
                    if (d.loaded && (d.conversations.isNotEmpty || d.archivedCustomers > 0))
                      _Chips(
                        filter: _filter,
                        unread: d.unreadCount,
                        archived: d.archivedCount,
                        onPick: (f) => setState(() => _filter = f),
                      ),
                    if (d.error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: TkErrorBox(d.error!)),
                    if (!archived && _filter != ChatFilter.starred)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: ChatRow(
                          accent: true,
                          title: 'Taskiem',
                          preview: d.waiting > 0
                              ? Text(d.waiting == 1 ? '1 thing needs you' : '${d.waiting} things need you',
                                  maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14.5, weight: 700, color: t.tintText))
                              : _previewText(context, 'Your own conversation with Taskiem, as on your phone'),
                        ),
                      ),
                    if (d.asked.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: _Asked(links: d.asked, manage: d.manage, now: d.now()),
                      ),
                    if (_filter != ChatFilter.starred && d.conversations.isNotEmpty)
                      Padding(
                        padding: EdgeInsets.only(left: 16, right: 16, top: d.asked.isEmpty ? 0 : 6),
                        child: Column(
                          children: [
                            for (final c in listed)
                              ChatRow(
                                title: c.name,
                                logo: c.logo ? d.logo(c.business) : null,
                                letter: c.name,
                                time: c.last == null ? null : listTime(c.last!.createdAt, d.now()),
                                ticks: c.last != null && c.last!.mine && c.last!.kind != 'card' ? (read: c.last!.readAt != null) : null,
                                preview: _previewText(
                                  context,
                                  c.typing
                                      ? 'typing…'
                                      : c.last != null
                                          ? listPreview(c.last!, c.name)
                                          : c.state == 'active'
                                              ? 'Linked'
                                              : 'The link has ended',
                                  typing: c.typing,
                                ),
                                unread: c.unread,
                                pinned: c.pinned,
                                muted: c.muted,
                              ),
                          ],
                        ),
                      ),
                  ],
                ),
                if (d.linksOn)
                  Positioned(
                    right: 16,
                    bottom: 16 + MediaQuery.paddingOf(context).bottom,
                    child: _FindButton(onTap: widget.onFind),
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
    return Text(s, maxLines: 1, overflow: TextOverflow.ellipsis, softWrap: false, style: tkText(size: 14.5, color: typing ? t.ok : t.muted));
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
          if (name != null) ...[
            const SizedBox(width: 6),
            Text(name!, style: tkText(size: 13, color: t.muted)),
          ],
          const SizedBox(width: 6),
          Semantics(
            button: true,
            label: 'More about the chats',
            child: TkPress(
              onTap: onMenu ?? () {},
              // The web's `more`, turned upright, its dots bolder (`.chat-menu-wrap svg`).
              child: SizedBox(width: 40, height: 40, child: Center(child: RotatedBox(quarterTurns: 1, child: TkIcon('more', size: 18, stroke: 3, color: t.text)))),
            ),
          ),
        ],
      ),
    );
  }
}

/// `.filters .search`: the round search box.
class _Search extends StatelessWidget {
  const _Search();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Container(
        height: 42,
        decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(999)),
        padding: const EdgeInsets.only(left: 11),
        child: Row(
          children: [
            TkIcon('search', size: 16, color: t.muted),
            const SizedBox(width: 7),
            Expanded(
              child: Text('Search chats, messages, businesses',
                  maxLines: 1, overflow: TextOverflow.clip, style: tkText(size: 15, weight: 500, color: cssMix(t.muted, const Color(0x00000000), 0.72))),
            ),
          ],
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
                child: Text(label, style: tkText(size: 14, weight: on ? 600 : 500, lineHeight: 1.2, color: on ? t.tintText : t.text)),
              ),
            ),
          );
        },
      ),
    ));
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
                        child: Text(title, maxLines: 1, overflow: TextOverflow.ellipsis, softWrap: false, style: tkText(size: 16, weight: 600, color: t.text)),
                      ),
                      if (time != null) ...[
                        const SizedBox(width: 8),
                        Text(time!, style: tkText(size: 12, weight: unread > 0 ? 600 : 400, color: unread > 0 ? t.ok : t.muted)),
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
                        Semantics(
                          label: '$unread unread ${unread == 1 ? 'message' : 'messages'}',
                          child: Container(
                            constraints: const BoxConstraints(minWidth: 20),
                            padding: const EdgeInsets.symmetric(horizontal: 7),
                            decoration: BoxDecoration(color: t.ok, borderRadius: BorderRadius.circular(999)),
                            alignment: Alignment.center,
                            child: Text('$unread', style: tkText(size: 12, weight: 600, color: t.panel)),
                          ),
                        ),
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
  const ChatAvatar({super.key, required this.name, this.logo, this.size = 52});
  final String name;
  final Uint8List? logo;
  final double size;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final l = logo;
    if (l != null) {
      return ClipOval(child: Image.memory(l, width: size, height: size, fit: BoxFit.cover, gaplessPlayback: true));
    }
    final (bg, ink) = toneColours(t, toneOf(name));
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: bg, shape: BoxShape.circle),
      alignment: Alignment.center,
      child: Text(initialsOf(name), style: tkText(size: size * 20 / 52, weight: 800, letterSpacing: 0.2, color: ink)),
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
class _Asked extends StatelessWidget {
  const _Asked({required this.links, required this.manage, required this.now});
  final List<BusinessLink> links;
  final bool manage;
  final DateTime now;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final badge = stateWords('asked_us')!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(top: 32, bottom: 12),
          child: Semantics(header: true, child: Text('Asks to link', style: tkText(size: 15, weight: 600, lineHeight: 1.35, letterSpacing: -0.15, color: t.text))),
        ),
        Container(
          margin: const EdgeInsets.only(bottom: 16),
          decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(20)),
          child: Column(
            children: [
              for (final l in links)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Container(
                        width: 40,
                        height: 40,
                        decoration: BoxDecoration(color: t.tintWarn, borderRadius: BorderRadius.circular(12)),
                        alignment: Alignment.center,
                        child: Text(l.name.isEmpty ? '' : String.fromCharCode(l.name.runes.first).toUpperCase(),
                            style: tkText(size: 16, weight: 700, color: t.tintWarnText)),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            SizedBox(
                              height: 25,
                              child: Row(
                                children: [
                                  Expanded(child: Text(l.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 15, weight: 600, color: t.text))),
                                  const SizedBox(width: 12),
                                  TkBadge(badge.label, tone: t.warn),
                                ],
                              ),
                            ),
                            const SizedBox(height: 3),
                            Text('${roleLine(l.role)} · asked ${whenWords(l.requestedAt, now)}', style: tkHint(t)),
                            const SizedBox(height: 3),
                            if (manage)
                              Padding(
                                padding: const EdgeInsets.only(top: 8),
                                child: Row(
                                  children: [
                                    TkSmallButton(label: 'Accept', primary: true, onPressed: () {}),
                                    const SizedBox(width: 8),
                                    TkSmallButton(label: 'Decline', onPressed: () {}),
                                    const SizedBox(width: 8),
                                    TkSmallButton(label: 'Block', danger: true, onPressed: () {}),
                                  ],
                                ),
                              )
                            else
                              Text('An owner or admin answers this.', style: tkHint(t)),
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
