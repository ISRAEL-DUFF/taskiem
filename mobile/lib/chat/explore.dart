// Find a business (web/src/pages/Explore.tsx, lib/explore.ts; kit.css
// .tk-x*, styles.css .explore-*, .carousel-*): a screen of its own over the
// chats. A search box, then cards (the business's logo and name, a line, a
// swipe of its photos with dots, its words, and Connect). Connect asks the
// business to link at once, as supplier and customer both; nothing is
// shared until it accepts. Only businesses that chose to be found are here.

import 'dart:async';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart' show TextInputAction;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'chats.dart';
import 'words.dart';

/// explore.ts cardOffer: what a card offers, from our link with it.
String? cardOffer(String state) => switch (state) {
  'none' || 'ended' || 'not_accepted' => 'connect',
  'requested' => 'requested',
  'active' => 'message',
  'asked_us' => 'asked',
  _ => null,
};

/// explore.ts cardLine: "Cement and sand · Ikeja"; one that delivers says
/// so, with what and where.
String exploreLine(ExploreCard c) {
  final line = [?c.tagline, ?c.area].where((s) => s.isNotEmpty).join(' · ');
  if (c.delivers != true) return line;
  final how = (c.vehicle ?? '').isNotEmpty ? 'Delivers by ${c.vehicle}' : 'Delivers';
  final where = (c.deliveryAreas ?? const []).take(3).join(', ');
  final now = switch (c.available) {
    'free' => 'Free now',
    'busy' => 'Busy',
    'off' => 'Off today',
    _ => '',
  };
  final hasTag = (c.tagline ?? '').isNotEmpty;
  return [hasTag ? line : how, hasTag ? how : (where.isNotEmpty ? where : (c.area ?? '')), now].where((s) => s.isNotEmpty).join(' · ');
}

/// The pictures Explore shows, fetched once each, by path.
class _Pictures extends ChangeNotifier {
  _Pictures(this.t);
  final Transport t;
  final _got = <String, Uint8List?>{};
  final _bad = <String>{};

  Uint8List? of(String path) {
    if (_got.containsKey(path)) return _got[path];
    _got[path] = null;
    t.bytes(path).then((b) {
      _got[path] = Uint8List.fromList(b);
      notifyListeners();
    }, onError: (_) {
      _bad.add(path);
      notifyListeners();
    });
    return null;
  }

  bool bad(String path) => _bad.contains(path);
}

String _photo(ExploreCard c, int n) => '/v1/explore/${c.business}/photos/$n?size=800&v=${c.v}';
String _logo(ExploreCard c) => '/v1/explore/${c.business}/logo?size=320&v=${c.v}';

/// Opens Find a business over the chats (`.tk-x { animation: tk-x-in 0.4s }`).
Future<void> showExplore(BuildContext context, {required ChatsData chats, required ValueChanged<LinkConversation> onOpen}) {
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return Navigator.of(context).push<void>(
    PageRouteBuilder<void>(
      transitionDuration: still ? Duration.zero : const Duration(milliseconds: 400),
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (context, _, _) => ExploreScreen(chats: chats, onOpen: onOpen),
      transitionsBuilder: (context, a, _, child) {
        final e = CurvedAnimation(parent: a, curve: const Cubic(0.2, 0.95, 0.25, 1.04));
        return FadeTransition(
          opacity: a,
          child: AnimatedBuilder(
            animation: e,
            builder: (context, child) => Transform.translate(
              offset: Offset(0, 24 * (1 - e.value)),
              child: Transform.scale(scale: 0.985 + 0.015 * e.value, child: child),
            ),
            child: child,
          ),
        );
      },
    ),
  );
}

class ExploreScreen extends StatefulWidget {
  const ExploreScreen({super.key, required this.chats, required this.onOpen});
  final ChatsData chats;

  /// A linked business's conversation, opened in Explore's place.
  final ValueChanged<LinkConversation> onOpen;

  @override
  State<ExploreScreen> createState() => _ExploreScreenState();
}

class _ExploreScreenState extends State<ExploreScreen> {
  final _q = TextEditingController();
  late final _pictures = _Pictures(widget.chats.auth.transport);
  ExploreResult? _start;
  ({String q, ExploreResult r})? _found;
  String? _error;
  Timer? _soon;
  bool _busy = false;
  String? _blindSent;

  /// Asked just now: the link the server gave back (Withdraw takes it),
  /// before the chats' list has heard.
  final _asked = <String, BusinessLink?>{};

  /// States changed here (a request withdrawn, an ask answered).
  final _states = <String, String>{};

  ChatsData get d => widget.chats;
  String get _typed => _q.text.trim();

  @override
  void initState() {
    super.initState();
    _q.addListener(_typing);
    d.api.explore().then((r) {
      if (mounted) setState(() => _start = r);
    }, onError: (Object e) {
      if (mounted) setState(() => _error = _say(e));
    });
  }

  @override
  void dispose() {
    _soon?.cancel();
    _q.dispose();
    _pictures.dispose();
    super.dispose();
  }

  String _say(Object e) => e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.';

  /// The words last searched for: the box also tells of the cursor moving.
  String _was = '';

  void _typing() {
    if (_q.text == _was) return;
    _was = _q.text;
    setState(() {});
    _soon?.cancel();
    final q = _typed;
    if (q.isEmpty) return;
    _soon = Timer(const Duration(milliseconds: 250), () {
      d.api.explore(q: q).then((r) {
        if (mounted) {
          setState(() {
            _found = (q: q, r: r);
            _error = null;
          });
        }
      }, onError: (Object e) {
        if (mounted) setState(() => _error = _say(e));
      });
    });
  }

  Future<void> _run(Future<String> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final said = await f();
      if (mounted) showTkToast(context, said);
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  void _connect(ExploreCard c) => _run(() async {
    final out = await d.auth.transport.send('POST', '/v1/links', body: {'business': c.business, 'role': 'both'});
    final link = out is Map && out['link'] is Map ? BusinessLink.fromJson((out['link'] as Map).cast<String, dynamic>()) : null;
    _asked[c.business] = link;
    unawaited(d.load());
    return 'Asked ${c.name} to link';
  });

  void _withdraw(ExploreCard c, BusinessLink l) => _run(() async {
    await d.auth.transport.send('POST', '/v1/links/${Uri.encodeComponent(l.id)}/cancel', body: const {});
    _asked.remove(c.business);
    _states[c.business] = 'none';
    unawaited(d.load());
    return 'Request withdrawn';
  });

  void _answer(ExploreCard c, BusinessLink l, String a) => _run(() async {
    final why = await d.answer(l, a);
    if (why != null) throw ApiError(0, {'error': why});
    _states[c.business] = a == 'accept' ? 'active' : 'none';
    return a == 'accept' ? 'Linked with ${c.name}' : 'Request declined';
  });

  void _blind(String shown) => _run(() async {
    final typed = _typed;
    await d.auth.transport.send('POST', '/v1/links', body: {'phone': typed, 'role': 'both'});
    _blindSent = typed;
    unawaited(d.load());
    return 'Request sent to $shown';
  });

  Future<void> _message(ExploreCard c) async {
    // The chat list may not have heard yet that they accepted.
    var conv = d.conversations.where((x) => x.business == c.business).firstOrNull;
    if (conv == null) {
      await d.load();
      conv = d.conversations.where((x) => x.business == c.business).firstOrNull;
    }
    if (!mounted) return;
    Navigator.of(context).pop();
    if (conv != null) widget.onOpen(conv);
  }

  Future<void> _more(String section) async {
    final s = _start;
    if (s == null) return;
    final have = switch (section) {
      'near' => s.near.length,
      'delivery' => s.delivery.length,
      _ => s.fresh.length,
    };
    setState(() => _busy = true);
    try {
      final r = await d.api.explore(section: section, offset: '$have');
      _start = switch (section) {
        'near' => _with(s, near: [...s.near, ...r.near], nearMore: r.nearMore),
        'delivery' => _with(s, delivery: [...s.delivery, ...r.delivery], deliveryMore: r.deliveryMore),
        _ => _with(s, fresh: [...s.fresh, ...r.fresh], more: r.more),
      };
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  /// The start page with a section grown by Show more; the rest as it was.
  ExploreResult _with(ExploreResult s, {List<ExploreCard>? near, bool? nearMore, List<ExploreCard>? delivery, bool? deliveryMore, List<ExploreCard>? fresh, bool? more}) =>
      ExploreResult(
        area: s.area,
        blind: s.blind,
        results: s.results,
        near: near ?? s.near,
        nearMore: nearMore ?? s.nearMore,
        delivery: delivery ?? s.delivery,
        deliveryMore: deliveryMore ?? s.deliveryMore,
        fresh: fresh ?? s.fresh,
        more: more ?? s.more,
      );

  Widget _card(ExploreCard c) {
    final state = _states[c.business] ?? c.state;
    final asked = _asked.containsKey(c.business);
    final pending = _asked[c.business] ?? d.links.where((l) => l.business == c.business && l.state == 'requested' && l.askedByUs).firstOrNull;
    final incoming = d.links.where((l) => l.business == c.business && l.state == 'asked_us').firstOrNull;
    return _Card(
      card: c,
      offer: asked ? 'requested' : cardOffer(state),
      manage: d.manage,
      busy: _busy,
      pictures: _pictures,
      canWithdraw: asked || pending != null,
      onConnect: () => _connect(c),
      onWithdraw: pending == null ? null : () => _withdraw(c, pending),
      onMessage: () => _message(c),
      onAnswer: incoming == null ? null : (a) => _answer(c, incoming, a),
    );
  }

  /// The page, drawn again whenever the chats change (a request to link
  /// arriving or answered changes what each card offers).
  @override
  Widget build(BuildContext context) => ListenableBuilder(listenable: d, builder: (context, _) => _page(context));

  Widget _page(BuildContext context) {
    final t = TkTheme.tokens(context);
    final pad = MediaQuery.paddingOf(context);
    final typed = _typed;
    final results = _found != null && _found!.q == typed ? _found!.r : null;
    final body = <Widget>[];
    Widget gap(double h) => SizedBox(height: h);
    Widget list(List<ExploreCard> cards) => Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < cards.length; i++) ...[if (i > 0) gap(16), _card(cards[i])],
      ],
    );
    if (_error != null) body.add(TkErrorBox(_error!));
    if (typed.isNotEmpty) {
      if (results == null) {
        body.add(Semantics(label: 'Searching businesses…', child: const SizedBox(height: 120)));
      } else if (results.blind != null) {
        final b = results.blind!;
        body.add(
          _Frame(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(4, 4, 4, 4),
              child: Row(
                children: [
                  Container(
                    width: 48,
                    height: 48,
                    decoration: BoxDecoration(color: t.brand, shape: BoxShape.circle),
                    alignment: Alignment.center,
                    child: TkIcon('chat', size: 20, color: t.brandDark),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('Send a link request to ${b.shown}', style: tkText(size: 16, weight: 700, color: t.text)),
                        Text(blindRowLine(b.kind), style: tkText(size: 13, color: t.muted)),
                      ],
                    ),
                  ),
                  if (d.manage) ...[
                    const SizedBox(width: 12),
                    if (_blindSent == typed)
                      Text('Request sent', style: tkText(size: 13, weight: 600, color: t.muted))
                    else
                      _Pill(label: 'Send request', onTap: _busy ? null : () => _blind(b.shown)),
                  ],
                ],
              ),
            ),
          ),
        );
      } else if (results.results.isEmpty) {
        body.add(_Empty('No business found for “$typed”. Only businesses that chose to be found are listed.'));
      } else {
        body.add(Semantics(container: true, label: 'Businesses found', explicitChildNodes: true, child: list(results.results)));
      }
    } else if (_start == null) {
      body.add(Semantics(label: 'Loading businesses…', child: const SizedBox(height: 120)));
    } else if (_start!.near.isEmpty && _start!.fresh.isEmpty && _start!.delivery.isEmpty) {
      body.add(_Empty('No businesses to show yet. Businesses appear here when they choose to be found.'));
    } else {
      final s = _start!;
      final sections = <(String title, String? sub, List<ExploreCard> cards, bool more, String section, String moreWords)>[
        if (s.near.isNotEmpty) ('Near you', s.area, s.near, s.nearMore, 'near', 'businesses near you'),
        if (s.delivery.isNotEmpty) ('Delivery', null, s.delivery, s.deliveryMore, 'delivery', 'delivery businesses'),
        if (s.fresh.isNotEmpty) ('New on Taskiem', null, s.fresh, s.more, 'new', 'new businesses'),
      ];
      for (var i = 0; i < sections.length; i++) {
        final (title, sub, cards, more, section, moreWords) = sections[i];
        if (i > 0) body.add(gap(24));
        body.add(
          Padding(
            // `.tk-x-sec { padding: 8px 2px 12px }`
            padding: const EdgeInsets.fromLTRB(2, 8, 2, 12),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Expanded(
                  child: Semantics(
                    header: true,
                    child: Text.rich(
                      TextSpan(
                        children: [
                          TextSpan(text: title),
                          if (sub != null) TextSpan(text: ' · $sub', style: tkText(size: 14, weight: 500, color: t.muted)),
                        ],
                      ),
                      style: tkText(size: 18, weight: 800, letterSpacing: -0.18, color: t.text),
                    ),
                  ),
                ),
                if (more)
                  Semantics(
                    button: true,
                    label: 'Show more $moreWords',
                    onTap: _busy ? null : () => _more(section),
                    excludeSemantics: true,
                    child: GestureDetector(
                      onTap: _busy ? null : () => _more(section),
                      child: SizedBox(
                        height: 36,
                        child: Center(
                          child: Text(
                            'Show more',
                            style: tkText(size: 14, weight: 600, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand),
                          ),
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        );
        body.add(list(cards));
      }
    }
    return ColoredBox(
      color: t.bg,
      child: Semantics(
        container: true,
        scopesRoute: true,
        namesRoute: true,
        explicitChildNodes: true,
        label: 'Find a business',
        child: Builder(
          builder: (context) => Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // `.tk-x-head`: back, the title, and the search under them.
              Padding(
                padding: EdgeInsets.fromLTRB(16, 14 > pad.top ? 14 : pad.top, 16, 12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Row(
                      children: [
                        TapArea(child: Semantics(
                          button: true,
                          label: 'Back to the chats',
                          onTap: () => Navigator.of(context).pop(),
                          excludeSemantics: true,
                          child: TkPress(
                            onTap: () => Navigator.of(context).pop(),
                            child: SizedBox(width: 40, height: 44, child: Center(child: TkIcon('back', size: 22, color: t.text))),
                          ),
                        )),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Semantics(
                            header: true,
                            child: Text('Find a business', style: tkText(size: 26, weight: 800, lineHeight: 1.35, letterSpacing: -0.52, color: t.text)),
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 10),
                    _SearchBox(controller: _q),
                  ],
                ),
              ),
              Expanded(
                child: ListView(
                  padding: EdgeInsets.fromLTRB(16, 4, 16, 28 + pad.bottom),
                  children: body,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// `.tk-x-search label`: the panel pill, 50 px, the glass, the words.
class _SearchBox extends StatelessWidget {
  const _SearchBox({required this.controller});
  final TextEditingController controller;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapField(child: Semantics(
      label: 'Search businesses',
      textField: true,
      child: Container(
        height: 50,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(16),
          boxShadow: const [BoxShadow(color: Color(0x140E1A14), offset: Offset(0, 1), blurRadius: 2)],
        ),
        child: Row(
          children: [
            TkIcon('search', size: 18, color: t.muted),
            const SizedBox(width: 10),
            Expanded(
              child: Material(
                type: MaterialType.transparency,
                child: TextField(
                  controller: controller,
                  textInputAction: TextInputAction.search,
                  cursorColor: t.text,
                  showCursor: !TkField.debugHideCaret,
                  style: tkText(size: 16, color: t.text),
                  decoration: InputDecoration.collapsed(
                    hintText: 'A name, what they sell, or an area',
                    hintStyle: tkText(size: 16, color: t.muted.withValues(alpha: t.muted.a * 0.72)),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    ));
  }
}

/// `.tk-x-card`: the panel, 24 px corners, lifted (a line round it in dark).
class _Frame extends StatelessWidget {
  const _Frame({required this.child, this.label});
  final Widget child;
  final String? label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(24),
          boxShadow: dark
              ? [BoxShadow(color: t.border, spreadRadius: 1)]
              : const [
                  BoxShadow(color: Color(0x0F0E1A14), offset: Offset(0, 1), blurRadius: 2),
                  BoxShadow(color: Color(0x400E1A14), offset: Offset(0, 12), blurRadius: 28, spreadRadius: -18),
                ],
        ),
        child: child,
      ),
    );
  }
}

/// `.tk-x-connect`: the pine pill (soft for Message), 40 px.
class _Pill extends StatelessWidget {
  const _Pill({required this.label, this.onTap, this.soft = false, this.semantics, this.height = 40, this.side = 16});
  final String label;
  final VoidCallback? onTap;
  final bool soft;
  final String? semantics;
  final double height;
  final double side;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = soft ? (t.tkSoft, dark ? t.text : t.brand) : (dark ? t.brandDark : t.brand, dark ? t.brandDarkText : t.tkOnPine);
    return TapArea(child: Semantics(
      button: true,
      label: semantics ?? label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        scale: 0.96,
        child: Container(
          height: height,
          padding: EdgeInsets.symmetric(horizontal: side),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
          child: Text(label, style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: ink)),
        ),
      ),
    ));
  }
}

/// Explore.tsx Card.
class _Card extends StatelessWidget {
  const _Card({
    required this.card,
    required this.offer,
    required this.manage,
    required this.busy,
    required this.pictures,
    required this.canWithdraw,
    required this.onConnect,
    required this.onMessage,
    this.onWithdraw,
    this.onAnswer,
  });
  final ExploreCard card;
  final String? offer;
  final bool manage;
  final bool busy;
  final _Pictures pictures;
  final bool canWithdraw;
  final VoidCallback onConnect;
  final VoidCallback onMessage;
  final VoidCallback? onWithdraw;
  final ValueChanged<String>? onAnswer;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = card;
    final line = exploreLine(c);
    final pill = CardPillWords;
    Widget? act;
    switch (offer) {
      case 'connect':
        act = manage
            ? _Pill(label: 'Connect', semantics: 'Connect with ${c.name}', onTap: busy ? null : onConnect)
            : pill('Not linked', lime: false, t: t);
      case 'requested':
        act = Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          mainAxisSize: MainAxisSize.min,
          children: [
            pill('Requested', lime: true, t: t),
            if (manage && canWithdraw) ...[
              const SizedBox(height: 2),
              Semantics(
                container: true,
                button: true,
                label: 'Withdraw request to ${c.name}',
                onTap: busy || onWithdraw == null ? null : onWithdraw,
                excludeSemantics: true,
                child: GestureDetector(
                  onTap: busy || onWithdraw == null ? null : onWithdraw,
                  child: SizedBox(
                    height: 24,
                    child: Center(
                      child: Text(
                        'Withdraw',
                        style: tkText(size: 12, weight: 500, color: t.muted).copyWith(decoration: TextDecoration.underline, decorationColor: t.muted),
                      ),
                    ),
                  ),
                ),
              ),
            ],
          ],
        );
      case 'message':
        act = _Pill(label: 'Message', semantics: 'Message ${c.name}', soft: true, onTap: onMessage);
      case 'asked':
        act = manage && onAnswer != null
            ? Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                mainAxisSize: MainAxisSize.min,
                children: [
                  _Pill(label: 'Accept', semantics: "Accept ${c.name}'s request to link", height: 36, side: 14, onTap: busy ? null : () => onAnswer!('accept')),
                  const SizedBox(height: 2),
                  Semantics(
                    container: true,
                    button: true,
                    label: 'Not now: say no to ${c.name}',
                    onTap: busy ? null : () => onAnswer!('decline'),
                    excludeSemantics: true,
                    child: GestureDetector(
                      onTap: busy ? null : () => onAnswer!('decline'),
                      child: SizedBox(
                        height: 24,
                        child: Center(
                          child: Text(
                            'Not now',
                            style: tkText(size: 12, weight: 500, color: t.muted).copyWith(decoration: TextDecoration.underline, decorationColor: t.muted),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              )
            : pill('Asked you', lime: true, t: t);
    }
    return _Frame(
      label: c.name,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // `.explore-head { padding: 4px 4px 12px; gap: 12px }`
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 12),
            child: Row(
              children: [
                ListenableBuilder(
                  listenable: pictures,
                  builder: (context, _) => ChatAvatar(name: c.name, logo: c.logo ? pictures.of(_logo(c)) : null, size: 48, letterSize: 16),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(c.name, style: tkText(size: 16, weight: 700, color: t.text)),
                      if (line.isNotEmpty)
                        Text(line, maxLines: 1, overflow: TextOverflow.ellipsis, softWrap: false, style: tkText(size: 13, color: t.muted)),
                    ],
                  ),
                ),
                if (act != null) ...[const SizedBox(width: 12), act],
              ],
            ),
          ),
          if (c.photos > 0) _Carousel(card: c, pictures: pictures),
          if ((c.about ?? '').isNotEmpty)
            Padding(
              // `.explore-about { margin: 6px 4px 4px; line-height: 1.5 }`, four lines at most.
              padding: const EdgeInsets.fromLTRB(4, 6, 4, 4),
              child: Text(
                c.about!,
                maxLines: 4,
                overflow: TextOverflow.ellipsis,
                style: tkText(size: 14, lineHeight: 1.5, color: cssMix(t.text, t.muted, 0.82)),
              ),
            ),
          if (!manage && (offer == 'connect' || offer == 'asked'))
            Padding(padding: const EdgeInsets.fromLTRB(4, 8, 4, 2), child: Text('An owner or admin can connect.', style: tkHint(t))),
          if (offer == 'asked' && manage)
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 8, 4, 2),
              child: Text('${c.name} asked to link with you. Accept to message, share price lists and order.', style: tkHint(t)),
            ),
        ],
      ),
    );
  }
}

/// kit.css `.tk-pill` (lime for Requested and Asked you).
// ignore: non_constant_identifier_names
Widget CardPillWords(String word, {required bool lime, required TkTokens t}) => Container(
  padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
  decoration: BoxDecoration(color: lime ? t.brandDark : t.tkSoft, borderRadius: BorderRadius.circular(999)),
  child: Text(word, maxLines: 1, style: tkText(size: 12, weight: 700, color: lime ? t.brandDarkText : t.text)),
);

/// Explore.tsx Carousel: the photos in a row that swipes, 210 px tall,
/// each with its words on it, and dots under them.
class _Carousel extends StatefulWidget {
  const _Carousel({required this.card, required this.pictures});
  final ExploreCard card;
  final _Pictures pictures;

  @override
  State<_Carousel> createState() => _CarouselState();
}

class _CarouselState extends State<_Carousel> {
  final _pages = PageController();
  int _at = 0;

  @override
  void dispose() {
    _pages.dispose();
    super.dispose();
  }

  void _go(int n) {
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (still) {
      _pages.jumpToPage(n);
    } else {
      _pages.animateToPage(n, duration: const Duration(milliseconds: 300), curve: Curves.easeInOut);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final c = widget.card;
    final count = c.photos;
    return Semantics(
      container: true,
      label: 'Photos of ${c.name}',
      explicitChildNodes: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(18),
            child: SizedBox(
              height: 210,
              child: PageView.builder(
                controller: _pages,
                itemCount: count,
                onPageChanged: (n) => setState(() => _at = n),
                itemBuilder: (context, n) {
                  final path = _photo(c, n);
                  final caption = n < c.captions.length ? c.captions[n] : '';
                  return Semantics(
                    image: true,
                    label: caption.isNotEmpty ? caption : 'Photo ${n + 1} of ${c.name}',
                    child: ListenableBuilder(
                      listenable: widget.pictures,
                      builder: (context, _) {
                        final bytes = widget.pictures.of(path);
                        return Stack(
                          fit: StackFit.expand,
                          children: [
                            ColoredBox(
                              color: t.tkTile,
                              child: widget.pictures.bad(path) ? Center(child: Opacity(opacity: 0.55, child: TkIcon('image', size: 56, color: t.muted))) : null,
                            ),
                            if (bytes != null) Image.memory(bytes, fit: BoxFit.cover, gaplessPlayback: true),
                            if (caption.isNotEmpty)
                              Positioned(
                                left: 12,
                                bottom: 12,
                                child: ConstrainedBox(
                                  constraints: BoxConstraints(maxWidth: MediaQuery.sizeOf(context).width * 0.6),
                                  child: ClipRRect(
                                    borderRadius: BorderRadius.circular(10),
                                    child: BackdropFilter(
                                      filter: ui.ImageFilter.blur(sigmaX: 3, sigmaY: 3),
                                      child: Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                                        color: t.tkPhotoChip,
                                        child: Text(
                                          caption,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          softWrap: false,
                                          style: tkText(size: 13, weight: 600, color: t.tkPhotoInk),
                                        ),
                                      ),
                                    ),
                                  ),
                                ),
                              ),
                          ],
                        );
                      },
                    ),
                  );
                },
              ),
            ),
          ),
          if (count > 1)
            Padding(
              // `.carousel-dots { padding-top: 6px; gap: 2px }`, each 24 px.
              padding: const EdgeInsets.only(top: 6),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  for (var n = 0; n < count; n++) ...[
                    if (n > 0) const SizedBox(width: 2),
                    TapArea(child: Semantics(
                      button: true,
                      selected: n == _at,
                      label: 'Show photo ${n + 1} of $count',
                      onTap: () => _go(n),
                      excludeSemantics: true,
                      child: GestureDetector(
                        onTap: () => _go(n),
                        child: SizedBox(
                          width: 24,
                          height: 24,
                          child: Center(
                            child: AnimatedContainer(
                              duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 300),
                              width: n == _at ? 22 : 7,
                              height: 7,
                              decoration: BoxDecoration(
                                color: n == _at ? (dark ? t.brandDark : t.brand) : t.borderStrong,
                                borderRadius: BorderRadius.circular(4),
                              ),
                            ),
                          ),
                        ),
                      ),
                    )),
                  ],
                ],
              ),
            ),
        ],
      ),
    );
  }
}

/// ui.tsx EmptyState: the glass on its tint, then the words.
class _Empty extends StatelessWidget {
  const _Empty(this.words);
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 56),
      decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(20)),
      child: Column(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(color: t.tint, borderRadius: BorderRadius.circular(16)),
            alignment: Alignment.center,
            child: TkIcon('search', size: 26, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand),
          ),
          const SizedBox(height: 14),
          Text(words, textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
        ],
      ),
    );
  }
}

/// Mentions.tsx: a business not linked, picked from "@", shown by its
/// Explore card (with Connect, Withdraw, or Accept); its conversation by
/// [onOpen] once linked.
Future<void> showExploreCard(BuildContext context, ChatsData chats, ExploreCard card, {ValueChanged<LinkConversation>? onOpen}) =>
    showTkModal<void>(context, title: card.name, builder: (_) => _OneCard(chats: chats, card: card, onOpen: onOpen));

class _OneCard extends StatefulWidget {
  const _OneCard({required this.chats, required this.card, this.onOpen});
  final ChatsData chats;
  final ExploreCard card;
  final ValueChanged<LinkConversation>? onOpen;

  @override
  State<_OneCard> createState() => _OneCardState();
}

class _OneCardState extends State<_OneCard> {
  late final _pictures = _Pictures(widget.chats.auth.transport);
  late String _state = widget.card.state;
  BusinessLink? _asked;
  bool _busy = false;
  String? _error;

  ChatsData get d => widget.chats;
  ExploreCard get c => widget.card;

  @override
  void dispose() {
    _pictures.dispose();
    super.dispose();
  }

  Future<void> _run(Future<String> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final said = await f();
      if (mounted) showTkToast(context, said);
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: d,
      builder: (context, _) {
        final pending = _asked ?? d.links.where((l) => l.business == c.business && l.state == 'requested' && l.askedByUs).firstOrNull;
        final incoming = d.links.where((l) => l.business == c.business && l.state == 'asked_us').firstOrNull;
        return Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _Card(
              card: c,
              offer: cardOffer(_state),
              manage: d.manage,
              busy: _busy,
              pictures: _pictures,
              canWithdraw: pending != null,
              onConnect: () => _run(() async {
                final out = await d.auth.transport.send('POST', '/v1/links', body: {'business': c.business, 'role': 'both'});
                _asked = out is Map && out['link'] is Map ? BusinessLink.fromJson((out['link'] as Map).cast<String, dynamic>()) : null;
                _state = 'requested';
                unawaited(d.load());
                return 'Asked ${c.name} to link';
              }),
              onWithdraw: pending == null
                  ? null
                  : () => _run(() async {
                      await d.auth.transport.send('POST', '/v1/links/${Uri.encodeComponent(pending.id)}/cancel', body: const {});
                      _asked = null;
                      _state = 'none';
                      unawaited(d.load());
                      return 'Request withdrawn';
                    }),
              onMessage: () async {
                var conv = d.conversations.where((x) => x.business == c.business).firstOrNull;
                if (conv == null) {
                  await d.load();
                  conv = d.conversations.where((x) => x.business == c.business).firstOrNull;
                }
                if (!context.mounted) return;
                Navigator.of(context).pop();
                if (conv != null) widget.onOpen?.call(conv);
              },
              onAnswer: incoming == null
                  ? null
                  : (a) => _run(() async {
                      final why = await d.answer(incoming, a);
                      if (why != null) throw ApiError(0, {'error': why});
                      _state = a == 'accept' ? 'active' : 'none';
                      return a == 'accept' ? 'Linked with ${c.name}' : 'Request declined';
                    }),
            ),
            if (_error != null) TkErrorBox(_error!),
          ],
        );
      },
    );
  }
}
