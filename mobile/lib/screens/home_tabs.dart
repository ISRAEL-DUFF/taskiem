// The bottom bar of the app's home (as WhatsApp's): Chats, Today, Money
// and Orders, the open one's icon in a pill (the web's `.tabbar`: lime on
// pine, pine on lime in the dark theme). Each is the app's own; a tab not
// built yet says it is coming (ComingTab). The bar shows on the home only: an open
// conversation takes the whole screen, as it does on WhatsApp.

import 'package:flutter/foundation.dart' show ValueListenable;
import 'dart:async';

import 'package:flutter/widgets.dart';

import '../chat/chats.dart' show UnreadCount;
import '../kit/icon.dart';
import '../theme/theme.dart';

/// A tab of the home: its name, its icon (theme/icons.g.dart), and what it
/// shows.
typedef HomeTab = ({String label, String icon, Widget Function(BuildContext) build});

class HomeTabs extends StatefulWidget {
  const HomeTabs({super.key, required this.tabs, this.count, this.counted, this.offline, this.updating});

  /// No connection, the pages showing what the phone kept: said in a
  /// strip over the bar, quietly, as WhatsApp's "Waiting for network".
  final ValueListenable<bool>? offline;

  /// How many kept pages are being brought up to date: "Updating…" in the
  /// same place, once it has taken more than a second.
  final ValueListenable<int>? updating;

  final List<HomeTab> tabs;

  /// The count on the first tab (Chats), when there is one; [counted] says
  /// when it changes.
  final int Function()? count;
  final Listenable? counted;

  @override
  State<HomeTabs> createState() => HomeTabsState();
}

class HomeTabsState extends State<HomeTabs> {
  int _open = 0;

  /// Where the last tab was, so the new one comes in from that side.
  int _was = 0;
  // A tab is built when first opened and kept (its place, its scrolling).
  final _built = <int>{0};

  int get open => _open;

  void show(int i) => setState(() {
    _was = _open;
    _open = i;
    _built.add(i);
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final pad = MediaQuery.paddingOf(context);
    return Column(
      children: [
        Expanded(
          // The bar takes the bottom: the screens above it do not pad for it.
          child: MediaQuery.removePadding(
            context: context,
            removeBottom: true,
            child: IndexedStack(
              index: _open,
              children: [
                for (var i = 0; i < widget.tabs.length; i++)
                  _built.contains(i)
                      ? _TabIn(on: i == _open, fromRight: i > _was, child: Builder(builder: widget.tabs[i].build))
                      : const SizedBox.shrink(),
              ],
            ),
          ),
        ),
        if (widget.offline != null)
          ValueListenableBuilder<bool>(
            valueListenable: widget.offline!,
            builder: (context, off, _) => AnimatedSize(
              duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 200),
              child: !off
                  ? const SizedBox(width: double.infinity)
                  : Semantics(
                      liveRegion: true,
                      container: true,
                      child: Container(
                        width: double.infinity,
                        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
                        color: t.tkSoft,
                        child: Text(
                          'No connection. Showing what was kept on this phone.',
                          textAlign: TextAlign.center,
                          style: tkText(size: 12.5, weight: 500, color: t.muted),
                        ),
                      ),
                    ),
            ),
          ),
        if (widget.updating != null)
          _Updating(updating: widget.updating!, offline: widget.offline),
        // `.tabbar { background: color-mix(panel 94%); border-top: 1px solid var(--border) }`
        DecoratedBox(
          decoration: BoxDecoration(
            color: t.panel,
            border: Border(top: BorderSide(color: t.border)),
          ),
          child: Padding(
            padding: EdgeInsets.only(left: pad.left, right: pad.right, bottom: pad.bottom),
            child: Row(
              children: [
                for (var i = 0; i < widget.tabs.length; i++)
                  Expanded(
                    child: _Tab(
                      tab: widget.tabs[i],
                      on: i == _open,
                      onTap: () => show(i),
                      count: i == 0 && widget.count != null
                          ? ListenableBuilder(
                              listenable: widget.counted ?? const _Never(),
                              builder: (context, _) {
                                final n = widget.count!();
                                return n <= 0 ? const SizedBox.shrink() : UnreadCount(n);
                              },
                            )
                          : null,
                    ),
                  ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

/// A tab coming into view: it fades in and slides a little from the side
/// of the bar it was chosen toward. Kept as it was while hidden.
class _TabIn extends StatefulWidget {
  const _TabIn({required this.on, required this.fromRight, required this.child});
  final bool on;
  final bool fromRight;
  final Widget child;

  @override
  State<_TabIn> createState() => _TabInState();
}

class _TabInState extends State<_TabIn> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 260), value: 1);
  late final _curve = CurvedAnimation(parent: _a, curve: Curves.easeOutCubic);

  @override
  void didUpdateWidget(_TabIn old) {
    super.didUpdateWidget(old);
    if (widget.on && !old.on) {
      final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
      if (still) {
        _a.value = 1;
      } else {
        _a.forward(from: 0);
      }
    }
  }

  @override
  void dispose() {
    _curve.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _curve,
    builder: (context, child) => Opacity(
      alwaysIncludeSemantics: true,
      opacity: 0.2 + 0.8 * _curve.value,
      child: FractionalTranslation(translation: Offset((widget.fromRight ? 0.06 : -0.06) * (1 - _curve.value), 0), child: child),
    ),
    child: widget.child,
  );
}

class _Never extends Listenable {
  const _Never();
  @override
  void addListener(VoidCallback listener) {}
  @override
  void removeListener(VoidCallback listener) {}
}

class _Tab extends StatelessWidget {
  const _Tab({required this.tab, required this.on, required this.onTap, this.count});
  final HomeTab tab;
  final bool on;
  final VoidCallback onTap;
  final Widget? count;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final pill = on ? (dark ? t.brandDark : t.brand) : const Color(0x00000000);
    final ink = on ? (dark ? t.brandDarkText : t.brandDark) : t.muted;
    return Semantics(
      button: true,
      selected: on,
      label: tab.label,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Padding(
          // Room above the icons and under the words, as WhatsApp's bar has.
          padding: const EdgeInsets.only(top: 10, bottom: 12),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Stack(
                clipBehavior: Clip.none,
                children: [
                  // The open tab's icon in its pill (`.tabbar .tab-icon`), a
                  // little larger, as WhatsApp's.
                  AnimatedContainer(
                    duration: still ? Duration.zero : const Duration(milliseconds: 220),
                    curve: Curves.easeOut,
                    width: 64,
                    height: 32,
                    decoration: BoxDecoration(color: pill, borderRadius: BorderRadius.circular(16)),
                    alignment: Alignment.center,
                    child: AnimatedScale(
                      scale: on ? 1 : 0.88,
                      duration: still ? Duration.zero : const Duration(milliseconds: 260),
                      curve: Curves.easeOutBack,
                      child: TkIcon(tab.icon, size: 24, color: ink),
                    ),
                  ),
                  // `.tab-count { top: -3px; right: 5px }`
                  if (count != null) Positioned(top: -4, right: 6, child: count!),
                ],
              ),
              const SizedBox(height: 4),
              Text(
                tab.label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: tkText(size: 12.5, weight: on ? 700 : 500, color: on ? t.text : t.muted),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A tab whose screens are not in the app yet: its name at the top, as the
/// chats' head, and that it is coming.
class ComingTab extends StatelessWidget {
  const ComingTab({super.key, required this.title, required this.icon, required this.words});
  final String title;
  final String icon;
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final top = MediaQuery.paddingOf(context).top;
    return ColoredBox(
      color: t.panel,
      child: Padding(
        padding: EdgeInsets.only(top: top),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              // As the chats' head.
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
              child: Semantics(
                header: true,
                child: Text(title, style: tkText(size: 26, weight: 800, lineHeight: 1.35, letterSpacing: -0.52, color: t.text)),
              ),
            ),
            Expanded(
              child: Center(
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 32),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Container(
                        width: 64,
                        height: 64,
                        decoration: BoxDecoration(color: t.tkSoft, shape: BoxShape.circle),
                        alignment: Alignment.center,
                        child: TkIcon(icon, size: 28, color: t.muted),
                      ),
                      const SizedBox(height: 16),
                      Text(words, textAlign: TextAlign.center, style: tkText(size: 15, color: t.text)),
                      const SizedBox(height: 6),
                      Text('This is coming to the app soon.', textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// "Updating…" over the bar while what was kept is brought up to date, once
/// that has taken more than a second (not while offline: that strip says so).
class _Updating extends StatefulWidget {
  const _Updating({required this.updating, this.offline});
  final ValueListenable<int> updating;
  final ValueListenable<bool>? offline;

  @override
  State<_Updating> createState() => _UpdatingState();
}

class _UpdatingState extends State<_Updating> {
  Timer? _wait;
  bool _show = false;

  @override
  void initState() {
    super.initState();
    widget.updating.addListener(_changed);
    widget.offline?.addListener(_offline);
    _changed();
  }

  // Offline now: that strip says so, this one goes.
  void _offline() {
    if (mounted) setState(() {});
  }

  void _changed() {
    if (widget.updating.value > 0) {
      _wait ??= Timer(const Duration(seconds: 1), () {
        if (mounted && widget.updating.value > 0) setState(() => _show = true);
      });
    } else {
      _wait?.cancel();
      _wait = null;
      if (_show && mounted) setState(() => _show = false);
    }
  }

  @override
  void dispose() {
    _wait?.cancel();
    widget.updating.removeListener(_changed);
    widget.offline?.removeListener(_offline);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final off = widget.offline?.value ?? false;
    return AnimatedSize(
      duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 200),
      child: !_show || off
          ? const SizedBox(width: double.infinity)
          : Semantics(
              liveRegion: true,
              container: true,
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
                color: t.tkSoft,
                child: Text('Updating…', textAlign: TextAlign.center, style: tkText(size: 12.5, weight: 500, color: t.muted)),
              ),
            ),
    );
  }
}
