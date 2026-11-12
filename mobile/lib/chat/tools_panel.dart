// The tools panel (web/src/pages/ChatTools.tsx ToolsPanel; chat.css
// `.tk-tools`): the grid over the composer from its tools button, or "/" and
// a word as the whole message. Its heading, a tile for each tool the person
// may open (its line and badge from the tool's list), the extra tiles of
// the conversation, and the last two used. In a business's conversation it
// holds what is between us and them (business-chat-tools.md §3).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError, Offline;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'chats.dart';
import 'tools.dart';

/// A tile that does something instead of opening a tool's sheet (Make an
/// offer, Statement, Clock in).
typedef ExtraTile = ({String key, String title, String icon, String line, VoidCallback onPick});

const _failed = Object();

/// What the panel and the sheets read, and whether the panel is open.
class ToolsController extends ChangeNotifier {
  ToolsController({required this.chats, this.scope, this.business});

  final ChatsData chats;

  /// The linked business's conversation it is in (its link): its tools only.
  final String? scope;

  /// Its name, for the heading ("With Bayo Rice Mills").
  final String? business;

  bool _open = false;
  String? _slash;

  /// Open: from the button, or "/" and a word as the whole message.
  bool get shown => chats.chatTools && (_open || _slash != null);

  /// What follows "/", when it was typed.
  String get query => _slash ?? '';

  void toggle() {
    _open = !_open;
    notifyListeners();
  }

  void close() {
    if (!_open && _slash == null) return;
    _open = false;
    notifyListeners();
  }

  /// The box's words: "/" and a word alone opens the panel filtered by it.
  void typed(String text) {
    final m = RegExp(r'^/(\S*)$').firstMatch(text);
    final q = m?.group(1);
    if (q == _slash) return;
    _slash = q;
    notifyListeners();
  }

  // --- the lists, one read for each url, shared by the panel and its sheets ---

  final _data = <String, Object?>{};
  final _asked = <String>{};
  Set<String>? _scopeIds;
  bool _askedScope = false;

  bool can(String perm) => perm.isEmpty || chats.auth.can(perm);
  List<String>? get roles => (chats.auth.me?.roles as List?)?.whereType<String>().toList();
  bool get delivers => chats.auth.me?.delivers ?? false;

  /// The tools this person may open here, matching [query].
  List<Tool> toolsHere([String? q]) => [
    for (final t in toolsFor(can, query: q ?? query, roles: roles, delivers: delivers))
      if (scope == null || businessTools.contains(t.key)) t,
  ];

  /// Reads each tool's list not read yet.
  void load(List<Tool> ts) {
    for (final url in {for (final t in ts) t.url}) {
      if (!_asked.add(url)) continue;
      unawaited(_read(url));
    }
    if (scope != null && !_askedScope) {
      _askedScope = true;
      unawaited(_readScope());
    }
  }

  Future<void> _read(String url) async {
    try {
      _data[url] = await chats.auth.transport.send('GET', url);
    } on ApiError {
      _data[url] = _failed;
    } on Offline {
      _data[url] = _failed;
    }
    notifyListeners();
  }

  Future<void> _readScope() async {
    try {
      final r = await chats.auth.transport.send('GET', '/v1/link-chats/${Uri.encodeComponent(scope!)}/refs');
      _scopeIds = {for (final x in ((r as Map?)?['refs'] as List?) ?? const []) if (x is Map && x['id'] is String) x['id'] as String};
    } on Object {
      _scopeIds = {};
    }
    notifyListeners();
  }

  /// Read again (Try again).
  void retry(Tool t) {
    _asked.remove(t.url);
    _data.remove(t.url);
    notifyListeners();
    load([t]);
  }

  /// Read again after a change, the list shown as it was meanwhile.
  void refresh(Tool t) {
    _asked.remove(t.url);
    load([t]);
  }

  bool failed(Tool t) => identical(_data[t.url], _failed);

  /// A tool's rows, here (in a business's conversation, those with it);
  /// null while they are read.
  List<ToolRow>? rowsOf(Tool t) {
    if (!_data.containsKey(t.url) || failed(t)) return null;
    return _inScope(t.rows(_data[t.url]), t.key);
  }

  /// ChatTools.tsx inScope: in a business's conversation the tools of what
  /// is between us show only what is with that business (a row of it, or
  /// one its conversation's things name), and nothing until those are
  /// known; another tool's rows (our price lists, to share) all stay.
  List<ToolRow>? _inScope(List<ToolRow> rows, String key) {
    final s = scope;
    if (s == null) return rows;
    final strict = const ['purchases', 'orders', 'bills', 'invoices'].contains(key);
    if (strict && _scopeIds == null) return null;
    return [
      for (final r in rows)
        if (strict ? (r.link == s || _scopeIds!.contains(r.id)) : (r.link == null || r.link == s)) r,
    ];
  }
}

/// The panel, floating from the top of the composer over the thread; the
/// tiles scroll inside it while its heading and Recent stay.
class ToolsPanel extends StatefulWidget {
  const ToolsPanel({super.key, required this.tools, required this.onOpen, this.lead = const [], this.extras = const [], this.recent = const [], this.maxHeight});

  final ToolsController tools;

  /// A tool's tile pressed.
  final ValueChanged<Tool> onOpen;

  /// Tiles before the tools (Their shop, Book), and after (Make an offer,
  /// Statement; Briefing, Tasks, Clock in).
  final List<ExtraTile> lead;
  final List<ExtraTile> extras;

  /// The last two used, by key.
  final List<String> recent;
  final double? maxHeight;

  @override
  State<ToolsPanel> createState() => _ToolsPanelState();
}

class _ToolsPanelState extends State<ToolsPanel> {
  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final c = widget.tools;
    final q = c.query.trim().toLowerCase();
    final listed = c.toolsHere();
    c.load(listed);
    bool named(ExtraTile x, [List<String> also = const []]) =>
        q.isEmpty || x.title.toLowerCase().startsWith(q) || also.any((w) => w.startsWith(q));
    final lead = [
      for (final x in widget.lead)
        if (named(x, x.key == 'order' ? const ['order', 'their shop', 'shop'] : const [])) x,
    ];
    final more = [for (final x in widget.extras) if (named(x)) x];
    final none = lead.isEmpty && listed.isEmpty && more.isEmpty;
    // The last used, as tiles of this panel.
    final recent = [
      for (final k in widget.recent)
        if (lead.any((x) => x.key == k))
          (title: lead.firstWhere((x) => x.key == k).title, open: lead.firstWhere((x) => x.key == k).onPick)
        else if (listed.any((x) => x.key == k))
          () {
            final tool = listed.firstWhere((x) => x.key == k);
            return (title: tool.title, open: () => widget.onOpen(tool));
          }(),
    ];
    return Semantics(
      container: true,
      label: 'Tools',
      explicitChildNodes: true,
      child: Container(
        // On a phone the tools sit on the message bar, flush, as one surface
        // (`.composer > .tk-tools { margin: 0; border-radius: 22px 22px 0 0;
        // padding: 12px 12px 10px }`).
        padding: const EdgeInsets.fromLTRB(12, 12, 12, 10),
        constraints: BoxConstraints(maxHeight: widget.maxHeight ?? double.infinity),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: const BorderRadius.vertical(top: Radius.circular(22)),
          boxShadow: const [BoxShadow(color: Color(0x590D3B2E), offset: Offset(0, -10), blurRadius: 30, spreadRadius: -18)],
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              // `.tk-tools-head { padding: 0 4px 12px }`, 8 px at phone width.
              padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.baseline,
                textBaseline: TextBaseline.alphabetic,
                children: [
                  Semantics(header: true, child: Text('Tools', style: tkText(size: 18, weight: 800, letterSpacing: -0.18, color: t.text))),
                  const Spacer(),
                  Flexible(
                    child: Text(
                      c.business != null ? 'With ${c.business}' : 'Opens over the chat',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: tkText(size: 12, color: t.muted),
                    ),
                  ),
                ],
              ),
            ),
            if (none)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
                child: Text('No tool called “${c.query}”. Send sends it as a message.', style: tkText(size: 12, color: t.muted)),
              )
            else
              Flexible(
                // `.tk-tools-grid { padding: 2px; margin: -2px }`: none, in all.
                child: SingleChildScrollView(
                  child: _Grid(
                    children: [
                      for (final x in lead) _Tile(title: x.title, icon: x.icon, line: x.line, onTap: x.onPick),
                      for (final tool in listed) _toolTile(tool),
                      for (final x in more) _Tile(title: x.title, icon: x.icon, line: x.line, onTap: x.onPick),
                    ],
                  ),
                ),
              ),
            if (recent.isNotEmpty && q.isEmpty)
              Container(
                // `.tk-tools-recent { margin-top: 8px; padding: 8px 6px 2px; border-top }`
                margin: const EdgeInsets.only(top: 8),
                padding: const EdgeInsets.fromLTRB(6, 8, 6, 2),
                decoration: BoxDecoration(border: Border(top: BorderSide(color: t.border))),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text('Recent', style: tkText(size: 13, color: t.muted)),
                    for (final r in recent)
                      TapArea(
                        child: Semantics(
                          button: true,
                          label: r.title,
                          excludeSemantics: true,
                          child: TkPress(
                            onTap: r.open,
                            child: Container(
                              constraints: const BoxConstraints(minHeight: 36),
                              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
                              decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(999)),
                              child: Text(r.title, style: tkText(size: 13, weight: 600, color: dark ? t.brandDark : t.brand)),
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _toolTile(Tool tool) {
    final c = widget.tools;
    final rows = c.rowsOf(tool);
    final line = c.failed(tool) ? 'Could not load' : (rows == null ? ' ' : tool.line(rows));
    final badge = rows == null ? null : tool.badge?.call(rows);
    return _Tile(title: tool.titleOf(rows), icon: tool.icon, line: line, badge: badge, onTap: () => widget.onOpen(tool));
  }
}

/// `.tk-tools-grid`: two to a row at phone width, 8 px apart.
class _Grid extends StatelessWidget {
  const _Grid({required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    const gap = 8.0;
    return LayoutBuilder(
      builder: (context, box) {
        final w = (box.maxWidth - gap) / 2;
        return Wrap(
          spacing: gap,
          runSpacing: gap,
          children: [for (final c in children) SizedBox(width: w, child: c)],
        );
      },
    );
  }
}

/// `.tk-tile` at phone width: a row, the icon on pine beside the name and
/// its line; the badge in the corner.
class _Tile extends StatelessWidget {
  const _Tile({required this.title, required this.icon, required this.line, this.badge, required this.onTap});
  final String title;
  final String icon;
  final String line;
  final ToolPill? badge;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: [title, if (line.trim().isNotEmpty) line, if (badge != null) badge!.text].join(', '),
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 60),
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(color: t.tkTile, borderRadius: BorderRadius.circular(16)),
          child: Stack(
            clipBehavior: Clip.none,
            children: [
              Row(
                children: [
                  // `.tk-tile-icon { width: 36px; height: 36px; border-radius: 11px; background: var(--brand); color: var(--brand-dark) }`
                  Container(
                    width: 36,
                    height: 36,
                    decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(11)),
                    alignment: Alignment.center,
                    child: TkIcon(icon, size: 20, color: t.brandDark),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        // A button's own line height (about 1.2), not the page's.
                        Text(title, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: t.text)),
                        Text(line, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12, lineHeight: 1.2, color: t.muted)),
                      ],
                    ),
                  ),
                ],
              ),
              if (badge != null) Positioned(top: -4, right: -4, child: TkPill(badge!.text, tone: badge!.tone, small: true)),
            ],
          ),
        ),
      ),
    );
  }
}

/// Where the panel floats: from the top of the composer, over the thread,
/// as tall as the room above it allows; a press above it closes it (its
/// button, in the composer, toggles it).
class ToolsAnchor extends StatefulWidget {
  const ToolsAnchor({super.key, required this.tools, required this.panel});
  final ToolsController tools;

  /// The panel, given the room it has.
  final Widget Function(BuildContext context, double room) panel;

  @override
  State<ToolsAnchor> createState() => _ToolsAnchorState();
}

class _ToolsAnchorState extends State<ToolsAnchor> {
  final _portal = OverlayPortalController();
  final _link = LayerLink();
  final _at = GlobalKey();

  @override
  void initState() {
    super.initState();
    _portal.show();
  }

  /// The composer's top on the screen.
  double _top() {
    final box = _at.currentContext?.findRenderObject();
    return box is RenderBox && box.hasSize ? box.localToGlobal(Offset.zero).dy : MediaQuery.sizeOf(context).height;
  }

  @override
  Widget build(BuildContext context) {
    return OverlayPortal(
      controller: _portal,
      overlayChildBuilder: (context) => ListenableBuilder(
        listenable: widget.tools,
        builder: (context, _) {
          if (!widget.tools.shown) return const SizedBox.shrink();
          final top = _top();
          final pad = MediaQuery.paddingOf(context);
          // The room above the composer, up to 12 px under the screen's top
          // (the page's, under the phone's bar), over the head too.
          final room = (top - pad.top - 12).clamp(160.0, double.infinity);
          return Stack(
            children: [
              Positioned(
                left: 0,
                right: 0,
                top: 0,
                height: top,
                child: GestureDetector(behavior: HitTestBehavior.translucent, onTap: widget.tools.close, excludeFromSemantics: true),
              ),
              Positioned(
                left: 0,
                top: 0,
                child: CompositedTransformFollower(
                  link: _link,
                  targetAnchor: Alignment.topLeft,
                  followerAnchor: Alignment.bottomLeft,
                  child: SizedBox(width: MediaQuery.sizeOf(context).width, child: widget.panel(context, room)),
                ),
              ),
            ],
          );
        },
      ),
      child: CompositedTransformTarget(link: _link, child: SizedBox(key: _at, width: double.infinity)),
    );
  }
}
