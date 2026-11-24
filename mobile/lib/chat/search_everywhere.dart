// Search everywhere (docs/spec/menus-everywhere.md §6, E4): one box for
// what Taskiem can do and what the business has. As the words are typed,
// the results in groups, three of each: Actions (the person's catalogue:
// "add rider" finds "Add a rider"), Chats (the businesses linked), Products,
// Services, Invoices and bills, Orders, Customers, Saved; a group's "Show
// more", or its chip, shows all of it. Invoices, orders, customers,
// services and saved things are searched on the server, every record (the
// picker's search, E1); a saved thing comes by its name and kind only, the
// person's own and their business's, never what it holds. The rest are the
// lists the phone already has. An action is done as from Today (its form
// first, its answer in a sheet); anything else opens where it is worked on.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkSearchBox;
import '../kit/mobile.dart' show TkChips, TkListRow, TkSection;
import '../kit/page.dart';
import '../kit/sheet.dart' show showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'bills_page.dart' show openBill;
import 'bookings_page.dart' show BookingsPage;
import 'chats.dart';
import 'invoices_page.dart' show openInvoice;
import 'menu_card.dart' show ActionFormSheet, catalogueOf, menuIcon, runActionHere, topicColor;
import 'purchases_page.dart' show openPurchase;
import 'saved_accounts.dart' show SavedAccountsPage, savedKinds;
import 'stock_page.dart' show StockPage;
import 'storefront_orders_page.dart' show openStorefrontOrder;
import 'tags.dart';
import 'ref_card.dart' show refStatusWords, shortDate;
import '../kit/mobile.dart' show TkRoute;

/// The kinds, as the chips say them; "all" first.
const searchKinds = {
  'all': 'All',
  'action': 'Actions',
  'chat': 'Chats',
  'product': 'Products',
  'service': 'Services',
  'invoice': 'Invoices',
  'order': 'Orders',
  'customer': 'Customers',
  'saved': 'Saved',
};

/// The server's kinds for a chip.
const _serverKinds = {'invoice': 'invoice,bill', 'order': 'order,purchase', 'customer': 'customer', 'service': 'service', 'saved': 'saved'};

/// All asks each of these on its own, so one kind's many never crowd out
/// another's few (services and saved things have no date to come first by).
const _allKinds = ['invoice,bill,order,purchase,customer', 'service', 'saved'];

/// Opens search everywhere over [context]; [onPath] opens a page of the app
/// by its web path (a conversation, a customer's chat).
Future<void> openSearchEverywhere(BuildContext context, ChatsData chats, {required ValueChanged<String> onPath}) =>
    Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => SearchEverywhere(chats: chats, onPath: onPath)));

class SearchEverywhere extends StatefulWidget {
  const SearchEverywhere({super.key, required this.chats, required this.onPath, this.debounce = const Duration(milliseconds: 250)});
  final ChatsData chats;
  final ValueChanged<String> onPath;
  final Duration debounce;

  @override
  State<SearchEverywhere> createState() => _SearchEverywhereState();
}

class _SearchEverywhereState extends State<SearchEverywhere> {
  final _box = TextEditingController();
  String _kind = 'all';
  Timer? _soon;

  List<ChatTopic> _topics = const [];
  List<StockItem> _stock = const [];

  /// What the server found for the words and the kind, and where it ended.
  ({String key, List<ChatRef> refs, String cursor})? _found;
  bool _offline = false;

  /// How many of a group show: three in All, eight more a "Show more".
  final _shown = <String, int>{};

  @override
  void initState() {
    super.initState();
    final auth = widget.chats.auth;
    unawaited(catalogueOf(auth).then((t) {
      if (mounted) setState(() => _topics = t);
    }, onError: (Object _) {}));
    if (auth.can('table.read')) {
      unawaited(auth.api.getStock().then((r) {
        if (mounted) setState(() => _stock = r.items);
      }, onError: (Object _) {}));
    }
    if (!widget.chats.loaded) unawaited(widget.chats.load().then((_) => mounted ? setState(() {}) : null, onError: (Object _) {}));
  }

  @override
  void dispose() {
    _soon?.cancel();
    _box.dispose();
    super.dispose();
  }

  String get _words => _box.text.trim();

  void _typed(String _) {
    setState(_shown.clear);
    _ask();
  }

  void _pick(String kind) {
    setState(() {
      _kind = kind;
      _shown.clear();
    });
    _ask();
  }

  /// Invoices, orders, customers, services and saved things: asked of the
  /// server a moment after the last key.
  void _ask() {
    _soon?.cancel();
    final words = _words;
    if (words.isEmpty) return;
    final all = _kind == 'all';
    final kinds = all ? _allKinds.join(',') : _serverKinds[_kind];
    if (kinds == null) return;
    final key = '$kinds|${words.toLowerCase()}';
    if (_found?.key == key) return;
    _soon = Timer(widget.debounce, () async {
      try {
        final api = widget.chats.api;
        final rs = await Future.wait([
          if (all)
            for (final (i, k) in _allKinds.indexed) api.chatRefs(q: words, kind: k, limit: i == 0 ? '30' : '10')
          else
            api.chatRefs(q: words, kind: kinds, limit: '20'),
        ]);
        if (!mounted || _words.toLowerCase() != words.toLowerCase()) return;
        setState(() {
          _found = (key: key, refs: [for (final r in rs) ...r.refs], cursor: all ? '' : (rs.first.cursor ?? ''));
          _offline = false;
        });
      } on Object {
        if (mounted) setState(() => _offline = true);
      }
    });
  }

  Future<void> _more(String group) async {
    final f = _found;
    setState(() => _shown[group] = (_shown[group] ?? (_kind == 'all' ? 3 : 8)) + 8);
    final kinds = _serverKinds[group];
    if (f == null || kinds == null || f.cursor.isEmpty || _kind == 'all') return;
    try {
      final r = await widget.chats.api.chatRefs(q: _words, kind: kinds, cursor: f.cursor, limit: '20');
      if (mounted && identical(_found, f)) setState(() => _found = (key: f.key, refs: [...f.refs, ...r.refs], cursor: r.cursor ?? ''));
    } on Object {
      if (mounted) setState(() => _offline = true);
    }
  }

  // --- what each group finds ---

  List<(TagSuggestion, ChatAction, ChatTopic)> _actions() {
    final out = <(TagSuggestion, ChatAction, ChatTopic)>[];
    final all = [
      for (final t in _topics)
        for (final a in t.actions) (TagSuggestion(kind: 'action', id: a.id, name: a.title, sub: a.line, category: t.name, words: a.shortcut), a, t),
    ];
    final found = suggest([for (final x in all) x.$1], _words, limit: null);
    for (final s in found) {
      out.add(all.firstWhere((x) => identical(x.$1, s)));
    }
    return out;
  }

  List<TagSuggestion> _chats() => suggest([
    for (final c in widget.chats.conversations)
      if (c.state == 'active') TagSuggestion(kind: 'business', id: c.link, name: c.name, sub: 'Linked business'),
  ], _words, limit: null);

  List<TagSuggestion> _products() => suggest(productRefs(_stock), _words, limit: null);

  /// Services and saved things found, as rows.
  List<TagSuggestion> _others(String kind) {
    final f = _found;
    if (f == null || !f.key.endsWith('|${_words.toLowerCase()}')) return const [];
    return [
      for (final r in f.refs)
        if (r.kind == kind) ?foundTag(r, kindOne: (k) => savedKinds[k]?.one),
    ];
  }

  List<TagSuggestion> _refs(Set<String> kinds) {
    final f = _found;
    final now = widget.chats.now();
    if (f == null || !f.key.endsWith('|${_words.toLowerCase()}')) return const [];
    return [for (final r in f.refs) if (kinds.contains(r.kind)) refTag(r, now, date: shortDate, status: refStatusWords)];
  }

  // --- opening ---

  Future<void> _openAction(ChatAction a) async {
    final auth = widget.chats.auth;
    final fields = a.fields ?? const [];
    if (fields.isEmpty) return runActionHere(context, auth, a.id, a.title, null);
    final values = await showTkSheet<Map<String, String>>(context, (_) => ActionFormSheet(auth: auth, action: a, initial: const {}));
    if (values == null || !mounted) return;
    final shown = [for (final f in fields) values[f.key] ?? ''].where((v) => v.isNotEmpty).toList();
    await runActionHere(context, auth, a.id, shown.isEmpty ? a.title : '${a.title}: ${shown.join(', ')}', values);
  }

  void _open(TagSuggestion s) {
    final auth = widget.chats.auth;
    switch (s.kind) {
      case 'business':
        widget.onPath('/chat?with=${Uri.encodeQueryComponent(s.id)}');
      case 'customer':
        widget.onPath('/chat?with=c-${Uri.encodeQueryComponent(s.id)}');
      case 'invoice':
        unawaited(openInvoice(context, auth, s.id));
      case 'bill':
        unawaited(openBill(context, auth, s.id));
      case 'order':
        openStorefrontOrder(context, auth, s.name);
      case 'purchase':
        unawaited(openPurchase(context, auth, s.id));
      case 'service':
        unawaited(Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => BookingsPage(auth: auth))));
      case 'saved':
        unawaited(Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => SavedAccountsPage(auth: auth, mine: s.savedOwner == 'mine'))));
      default:
        unawaited(Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => StockPage(auth: auth, backLabel: 'Back to Search'))));
    }
  }

  // --- drawing ---

  Widget _group(String key, String title, List<Widget> rows) {
    if (rows.isEmpty) return const SizedBox.shrink();
    final n = _shown[key] ?? (_kind == 'all' ? 3 : 8);
    final serverMore = _kind != 'all' && _serverKinds.containsKey(key) && (_found?.cursor.isNotEmpty ?? false);
    final more = rows.length > n || serverMore;
    return Column(
      key: ValueKey('search-$key'),
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkSection(title: title, inset: 56, children: rows.take(n).toList()),
        if (more)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Center(
              child: TkSmallButton(
                label: 'Show more',
                semantics: 'Show more $title',
                onPressed: () {
                  if (_kind == 'all') {
                    _pick(key);
                  } else {
                    unawaited(_more(key));
                  }
                },
              ),
            ),
          ),
      ],
    );
  }

  TkListRow _row(TagSuggestion s, String icon, Color square) => TkListRow(
    title: s.name,
    sub: s.sub ?? '',
    subLines: 1,
    titleLines: 1,
    icon: icon,
    square: square,
    onTap: () => _open(s),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final words = _words;
    bool on(String k) => _kind == 'all' || _kind == k;
    final groups = words.isEmpty
        ? const <Widget>[]
        : <Widget>[
            if (on('action'))
              _group('action', 'Actions', [
                for (final (s, a, tp) in _actions())
                  TkListRow(
                    title: s.name,
                    sub: '${tp.name} · ${s.sub}',
                    subLines: 1,
                    titleLines: 1,
                    icon: menuIcon(a.icon),
                    square: topicColor(look, tp.color),
                    onTap: () => unawaited(_openAction(a)),
                  ),
              ]),
            if (on('chat')) _group('chat', 'Chats', [for (final s in _chats()) _row(s, 'b-chat', look.teal)]),
            if (on('product')) _group('product', 'Products', [for (final s in _products()) _row(s, 'b-box', look.lime)]),
            if (on('service')) _group('service', 'Services', [for (final s in _others('service')) _row(s, 'b-clock', look.teal)]),
            if (on('invoice')) _group('invoice', 'Invoices and bills', [for (final s in _refs({'invoice', 'bill'})) _row(s, 'b-receipt', look.blue)]),
            if (on('order')) _group('order', 'Orders', [for (final s in _refs({'order', 'purchase'})) _row(s, 'b-box', look.orange)]),
            if (on('customer')) _group('customer', 'Customers', [for (final s in _refs({'customer'})) _row(s, 'b-person', look.violet)]),
            if (on('saved'))
              _group('saved', 'Saved', [
                for (final s in _others('saved')) _row(s, s.savedKind == 'custom' ? 'b-box' : savedKinds[s.savedKind]?.icon ?? 'b-note', look.orange),
              ]),
          ];
    final nothing = words.isNotEmpty && groups.every((g) => g is SizedBox) && _found != null;
    return TkPageScreen(
      title: 'Search',
      titleSize: 30,
      backLabel: 'Back',
      under: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkSearchBox(
            controller: _box,
            hint: 'Actions, products, invoices, chats…',
            label: 'Search everything',
            onChanged: _typed,
            margin: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          ),
          TkChips<String>(
            label: 'Kinds',
            chips: [for (final e in searchKinds.entries) (e.key, e.value)],
            on: _kind,
            onPick: _pick,
          ),
        ],
      ),
      children: [
        if (words.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 12, 4, 0),
            child: Text(
              'Search what Taskiem can do ("add rider", "sell"), your products, services, invoices, orders, customers, chats and saved things.',
              style: tkText(size: 14, lineHeight: 1.45, color: t.muted),
            ),
          ),
        if (_offline)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
            child: Text('Invoices, orders, customers, services and saved things could not be searched. Check your connection.', style: tkText(size: 13, color: t.muted)),
          ),
        ...groups.where((g) => g is! SizedBox),
        if (nothing)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 12, 4, 0),
            child: Text('Nothing found for "$words".', style: tkText(size: 14, color: t.muted)),
          ),
      ],
    );
  }
}
