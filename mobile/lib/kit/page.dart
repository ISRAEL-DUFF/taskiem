// The app's screens for the web's full pages (Stock, Bookings, Price
// lists; Linked businesses): the head with Back and the title, the page's
// line under it, its cards (`.card`), and the pine hero (`.hero`).

import 'package:flutter/services.dart';
import 'package:flutter/material.dart' show RefreshIndicator;
import 'package:flutter/widgets.dart';

import '../chat/thread.dart' show ChatHeadBar;
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';

/// A page as a screen: its head (Back, the title, actions), and its body
/// scrolling, the page's description first.
class TkPageScreen extends StatelessWidget {
  const TkPageScreen({super.key, required this.title, required this.children, this.description, this.backLabel = 'Back', this.action, this.onRefresh, this.back = true});
  final String title;

  /// False for a page that is a tab of its own (the Money tab): no Back.
  final bool back;
  final String? description;
  final String backLabel;

  /// The page's own button, at the head's end (Add items).
  final Widget? action;
  final List<Widget> children;

  /// Pulled down: read again.
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    // All built, none dropped when scrolled away: a card keeps what was
    // typed in it (a page is a few cards, not a long list).
    Widget list = SingleChildScrollView(
      physics: onRefresh == null ? null : const AlwaysScrollableScrollPhysics(),
      padding: EdgeInsets.fromLTRB(16, 16, 16, 24 + pad.bottom),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (description != null) ...[Text(description!, style: tkHint(t)), const SizedBox(height: 16)],
          for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 16), children[i]],
        ],
      ),
    );
    if (onRefresh != null) list = RefreshIndicator(onRefresh: onRefresh!, color: t.brand, child: list);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ColoredBox(
        color: t.bg,
        child: Column(
          children: [
            ColoredBox(
              color: t.panel,
              child: Padding(
                padding: EdgeInsets.fromLTRB(back ? 8 : 16, 10 + pad.top, 16, 10),
                child: Row(
                  children: [
                    if (back) ...[
                      ChatHeadBar.iconButton(backLabel, TkIcon('back', size: 20, color: t.text), () => Navigator.of(context).maybePop()),
                      const SizedBox(width: 6),
                    ],
                    Expanded(
                      child: Semantics(
                        header: true,
                        child: Text(title, style: tkText(size: 17, weight: 600, color: t.text)),
                      ),
                    ),
                    ?action,
                  ],
                ),
              ),
            ),
            Expanded(child: list),
          ],
        ),
      ),
    );
  }
}

/// The web's `.card`: a panel, its title, what it holds.
class TkPageCard extends StatelessWidget {
  const TkPageCard({super.key, this.title, required this.children, this.trailing});
  final String? title;
  final List<Widget> children;

  /// Beside the title (a filter, Every item).
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
        decoration: BoxDecoration(
          color: t.panel,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(Tk.radius),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (title != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Row(
                  children: [
                    Expanded(
                      child: Semantics(
                        header: true,
                        child: Text(title!, style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text)),
                      ),
                    ),
                    ?trailing,
                  ],
                ),
              ),
            ...children,
          ],
        ),
      ),
    );
  }
}

/// The web's `.hero`: pine, a label and its tag, the amount large, a line,
/// and its button.
class TkHero extends StatelessWidget {
  const TkHero({super.key, required this.label, required this.amount, this.sub, this.tag, this.action, this.semanticsLabel});
  final String label;
  final String amount;
  final String? sub;
  final String? tag;
  final Widget? action;
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    const white = Color(0xFFFFFFFF);
    final soft = white.withValues(alpha: 0.8);
    return Semantics(
      container: true,
      label: semanticsLabel,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 20, 20, 16),
        decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(24)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(label, style: tkText(size: 14, weight: 500, color: soft)),
                ),
                if (tag != null)
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
                    decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(999)),
                    child: Text(tag!, style: tkText(size: 12, weight: 700, color: t.brandDarkText)),
                  ),
              ],
            ),
            const SizedBox(height: 2),
            Text(amount, style: tkText(size: 40, weight: 700, lineHeight: 1.2, letterSpacing: -0.8, color: white)),
            if (sub != null) ...[const SizedBox(height: 2), Text(sub!, style: tkText(size: 13, color: soft))],
            if (action != null) ...[const SizedBox(height: 12), action!],
          ],
        ),
      ),
    );
  }
}

/// A form in a modal: its fields, an error, and its buttons (the primary
/// first, then Cancel), as the web's `.toolbar`.
class TkModalForm extends StatelessWidget {
  const TkModalForm({super.key, required this.children, required this.save, required this.saveWords, this.problem, this.error, this.busy = false});
  final List<Widget> children;
  final VoidCallback? save;
  final String saveWords;
  final String? problem;
  final String? error;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final c in children) Padding(padding: const EdgeInsets.only(bottom: 14), child: c),
        if (problem != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Text(problem!, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(error!)),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(label: saveWords, primary: true, enabled: !(busy || problem != null), onPressed: save),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}
