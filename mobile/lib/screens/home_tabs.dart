// The bottom bar of the app's home (as WhatsApp's): Chats, Updates, Money
// and Orders, the open one's icon in a pill (the web's `.tabbar`: lime on
// pine, pine on lime in the dark theme). Only Chats is in the app yet; the
// others say they are coming. The bar shows on the home only: an open
// conversation takes the whole screen, as it does on WhatsApp.

import 'package:flutter/widgets.dart';

import '../chat/chats.dart' show UnreadCount;
import '../kit/icon.dart';
import '../theme/theme.dart';

/// A tab of the home: its name, its icon (theme/icons.g.dart), and what it
/// shows.
typedef HomeTab = ({String label, String icon, Widget Function(BuildContext) build});

class HomeTabs extends StatefulWidget {
  const HomeTabs({super.key, required this.tabs, this.count, this.counted});

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
  // A tab is built when first opened and kept (its place, its scrolling).
  final _built = <int>{0};

  int get open => _open;

  void show(int i) => setState(() {
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
                  _built.contains(i) ? Builder(builder: widget.tabs[i].build) : const SizedBox.shrink(),
              ],
            ),
          ),
        ),
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
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: ConstrainedBox(
          // `.tabbar a { min-height: 56px; gap: 3px; font-size: 11.5px }`
          constraints: const BoxConstraints(minHeight: 56),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Stack(
                clipBehavior: Clip.none,
                children: [
                  // `.tabbar .tab-icon { width: 56px; height: 30px; border-radius: 15px }`
                  AnimatedContainer(
                    duration: still ? Duration.zero : const Duration(milliseconds: 150),
                    width: 56,
                    height: 30,
                    decoration: BoxDecoration(color: pill, borderRadius: BorderRadius.circular(15)),
                    alignment: Alignment.center,
                    child: TkIcon(tab.icon, size: 22, color: ink),
                  ),
                  // `.tab-count { top: -3px; right: 5px }`
                  if (count != null) Positioned(top: -3, right: 5, child: count!),
                ],
              ),
              const SizedBox(height: 3),
              Text(
                tab.label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: tkText(size: 11.5, weight: on ? 600 : 500, color: on ? t.text : t.muted),
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
