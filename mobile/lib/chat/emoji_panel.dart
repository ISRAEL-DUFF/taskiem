// The emoji panel (web/src/pages/EmojiPanel.tsx; styles.css .emoji-panel,
// .emoji-search, .emoji-tabs, .emoji-tones, .emoji-grid): a search box, the
// recently used first, then the groups, each emoji a button named in words,
// a skin tone for the hands. The menu's "+" shows it in the reactions'
// place (`.pop-picker`).

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart' show TextInputAction;
import 'package:flutter/widgets.dart';

import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'emoji.dart';

/// What was picked lately and the tone chosen, kept while the app runs (the
/// web keeps them in the browser).
class EmojiMemory {
  static List<String> recent = [];
  static int tone = 0;
}

const _tones = [
  (0, 'Default skin tone', '🖐️'),
  (1, 'Light skin tone', '🖐🏻'),
  (2, 'Medium-light skin tone', '🖐🏼'),
  (3, 'Medium skin tone', '🖐🏽'),
  (4, 'Medium-dark skin tone', '🖐🏾'),
  (5, 'Dark skin tone', '🖐🏿'),
];

/// `filter: grayscale(1)`: the luminance of each colour (CSS's weights).
const _grey = ColorFilter.matrix([
  0.2126, 0.7152, 0.0722, 0, 0, //
  0.2126, 0.7152, 0.0722, 0, 0,
  0.2126, 0.7152, 0.0722, 0, 0,
  0, 0, 0, 1, 0,
]);

class EmojiPanel extends StatefulWidget {
  const EmojiPanel({super.key, required this.onPick, this.maxHeight = 280});
  final ValueChanged<String> onPick;

  /// `.emoji-panel { max-height: 280px }`; 330 in the menu.
  final double maxHeight;

  @override
  State<EmojiPanel> createState() => _EmojiPanelState();
}

class _EmojiPanelState extends State<EmojiPanel> {
  final _q = TextEditingController();
  final _grid = ScrollController();
  late List<String> _recent = EmojiMemory.recent;
  late int _tone = EmojiMemory.tone;
  late String _group = _recent.isNotEmpty ? 'recent' : emojiGroups.first.id;

  @override
  void initState() {
    super.initState();
    _q.addListener(
      () => setState(() {
        if (_grid.hasClients) _grid.jumpTo(0);
      }),
    );
  }

  @override
  void dispose() {
    _q.dispose();
    _grid.dispose();
    super.dispose();
  }

  List<({String char, String name})> get _items {
    if (_q.text.trim().isNotEmpty) return [for (final e in searchEmoji(_q.text)) (char: withTone(e.char, _tone), name: e.name)];
    if (_group == 'recent') return [for (final c in _recent) (char: c, name: emojiName(c) ?? c)];
    final g = emojiGroups.where((g) => g.id == _group).firstOrNull;
    return [for (final e in g?.items ?? const <EmojiItem>[]) (char: withTone(e.char, _tone), name: e.name)];
  }

  void _pick(String c) {
    EmojiMemory.recent = _recent = addRecent(_recent, c);
    widget.onPick(c);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final items = _items;
    final searching = _q.text.trim().isNotEmpty;
    final tabs = [
      if (_recent.isNotEmpty) (id: 'recent', label: 'Recently used', icon: '🕘'),
      for (final g in emojiGroups) (id: g.id, label: g.label, icon: g.icon),
    ];
    final hands = items.any((i) => takesTone(i.char));
    return Semantics(
      container: true,
      label: 'Emoji',
      child: Container(
        constraints: BoxConstraints(maxHeight: widget.maxHeight),
        color: t.panel,
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _Search(controller: _q),
            if (!searching) ...[
              const SizedBox(height: 6),
              Semantics(
                container: true,
                label: 'Kinds of emoji',
                child: SizedBox(
                  height: 34,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    children: [
                      for (final g in tabs) ...[
                        if (g != tabs.first) const SizedBox(width: 2),
                        _Tab(
                          icon: g.icon,
                          label: g.label,
                          on: _group == g.id,
                          onTap: () => setState(() {
                            _group = g.id;
                            if (_grid.hasClients) _grid.jumpTo(0);
                          }),
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ],
            if (hands) ...[
              const SizedBox(height: 6),
              Semantics(
                container: true,
                label: 'Skin tone',
                child: Row(
                  children: [
                    for (final (n, label, swatch) in _tones) ...[
                      if (n > 0) const SizedBox(width: 2),
                      _Tone(swatch: swatch, label: label, on: _tone == n, onTap: () => setState(() => EmojiMemory.tone = _tone = n)),
                    ],
                  ],
                ),
              ),
            ],
            const SizedBox(height: 6),
            Flexible(
              child: Semantics(
                container: true,
                label: searching ? 'Found' : (tabs.where((g) => g.id == _group).firstOrNull?.label ?? 'Emoji'),
                child: items.isEmpty
                    ? Text('No emoji for that.', style: tkText(size: 13, color: t.muted))
                    : LayoutBuilder(
                        builder: (context, box) {
                          // `repeat(auto-fill, minmax(38px, 1fr))` with a 2 px gap.
                          final cols = ((box.maxWidth + 2) / 40).floor().clamp(1, 99);
                          return GridView.builder(
                            controller: _grid,
                            padding: EdgeInsets.zero,
                            shrinkWrap: true,
                            gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                              crossAxisCount: cols,
                              mainAxisExtent: 38,
                              mainAxisSpacing: 2,
                              crossAxisSpacing: 2,
                            ),
                            itemCount: items.length,
                            itemBuilder: (context, i) => _Emoji(char: items[i].char, name: items[i].name, onTap: () => _pick(items[i].char)),
                          );
                        },
                      ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// `.emoji-search`: the soft field, 36 px, "Search emoji".
class _Search extends StatelessWidget {
  const _Search({required this.controller});
  final TextEditingController controller;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapField(child: Container(
      height: 36,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      alignment: Alignment.centerLeft,
      decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(Tk.radiusSm)),
      child: Material(
        type: MaterialType.transparency,
        child: TextField(
          controller: controller,
          textInputAction: TextInputAction.search,
          cursorColor: t.text,
          style: tkText(size: 14, color: t.text),
          decoration: InputDecoration.collapsed(
            hintText: 'Search emoji',
            hintStyle: tkText(size: 14, color: t.muted.withValues(alpha: t.muted.a * 0.72)),
          ),
        ),
      ),
    ));
  }
}

/// `.emoji-tab`: a group's icon, grey until it is the one shown.
class _Tab extends StatelessWidget {
  const _Tab({required this.icon, required this.label, required this.on, required this.onTap});
  final String icon;
  final String label;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget glyph = Text(icon, style: tkText(size: 18, lineHeight: 1.2));
    if (!on) {
      glyph = Opacity(opacity: 0.7, child: ColorFiltered(colorFilter: _grey, child: glyph));
    }
    return Semantics(
      button: true,
      selected: on,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          width: 36,
          height: 34,
          alignment: Alignment.center,
          decoration: BoxDecoration(color: on ? t.chatSelect : null, borderRadius: BorderRadius.circular(8)),
          child: glyph,
        ),
      ),
    );
  }
}

/// `.emoji-tone`: a hand in each tone, ringed when chosen.
class _Tone extends StatelessWidget {
  const _Tone({required this.swatch, required this.label, required this.on, required this.onTap});
  final String swatch;
  final String label;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      selected: on,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        onTap: onTap,
        child: Opacity(
          opacity: on ? 1 : 0.75,
          child: Container(
            width: 32,
            height: 30,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: on ? t.chatSelect : null,
              borderRadius: BorderRadius.circular(8),
              border: on ? Border.all(color: t.accent, width: 2) : null,
            ),
            child: Text(swatch, style: tkText(size: 16, lineHeight: 1.2)),
          ),
        ),
      ),
    );
  }
}

/// `.emoji`: 24 px in a 38 px cell, named in words.
class _Emoji extends StatelessWidget {
  const _Emoji({required this.char, required this.name, required this.onTap});
  final String char;
  final String name;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: name,
      onTap: onTap,
      // A key of a keyboard, as the web's 38 px cells: the grid is the
      // emoji keyboard, not padded to 48 each (they would overlap).
      identifier: 'emoji-key',
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Center(child: Text(char, style: tkText(size: 24, lineHeight: 1))),
      ),
    );
  }
}
