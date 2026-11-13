// The app's screens for the pages away from the chat (Stock, Bookings,
// Price lists, invoices…), in the app's own look: the head with Back, the
// title and its buttons; its parts as titled panels; the pine summary.

import 'package:flutter/services.dart';
import 'package:flutter/material.dart' show RefreshIndicator;
import 'package:flutter/widgets.dart';

import '../chat/thread.dart' show ChatHeadBar;
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'mobile.dart' show TkEnter, TkHeadButton;
import 'modal.dart' show showTkModal;

/// A page as a screen, in the app's own look (mobile.dart): the head
/// (Back, the title, its buttons; on a tab the title large), and its parts
/// rising in one after another. The page's description is not a paragraph
/// at the top: it is behind the head's (i), for whoever wants it.
class TkPageScreen extends StatelessWidget {
  const TkPageScreen({super.key, required this.title, required this.children, this.description, this.backLabel = 'Back', this.action, this.onRefresh, this.back = true, this.under});
  final String title;

  /// Stays under the head as the page scrolls (chips to filter by).
  final Widget? under;

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
      physics: onRefresh == null ? const BouncingScrollPhysics() : const AlwaysScrollableScrollPhysics(),
      padding: EdgeInsets.fromLTRB(16, 8, 16, 28 + pad.bottom),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[
            if (i > 0) const SizedBox(height: 20),
            TkEnter(index: i, child: children[i]),
          ],
        ],
      ),
    );
    if (onRefresh != null) list = RefreshIndicator(onRefresh: onRefresh!, color: t.brand, child: list);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: TkAppLook(
        child: ColoredBox(
        color: t.bg,
        child: Column(
          children: [
            Padding(
              padding: EdgeInsets.fromLTRB(back ? 6 : 20, 8 + pad.top, 12, back ? 6 : 4),
              child: Row(
                children: [
                  if (back) ...[
                    ChatHeadBar.iconButton(backLabel, TkIcon('back', size: 22, color: t.text), () => Navigator.of(context).maybePop()),
                    const SizedBox(width: 4),
                  ],
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text(
                        title,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: back ? 19 : 28, weight: 700, lineHeight: 1.2, letterSpacing: back ? -0.2 : -0.6, color: t.text),
                      ),
                    ),
                  ),
                  if (description != null)
                    TkHeadButton(
                      icon: 'info',
                      label: 'About this page',
                      onTap: () => showTkModal<void>(
                        context,
                        title: title,
                        builder: (context) => Text(description!, style: tkText(size: 15, lineHeight: 1.45, color: TkTheme.tokens(context).text)),
                      ),
                    ),
                  if (action != null) ...[const SizedBox(width: 6), Flexible(child: action!)],
                ],
              ),
            ),
            ?under,
            Expanded(child: list),
          ],
        ),
      ),
      )
    );
  }
}

/// A part of a page: its title over a rounded panel holding what it holds.
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
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (title != null)
            Padding(
              padding: const EdgeInsets.only(left: 4, right: 2, bottom: 8),
              child: Row(
                children: [
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text(title!, style: tkText(size: 17, weight: 700, lineHeight: 1.3, letterSpacing: -0.2, color: t.text)),
                    ),
                  ),
                  ?trailing,
                ],
              ),
            ),
          Container(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
            decoration: BoxDecoration(
              color: t.panel,
              border: Border.all(color: t.border),
              borderRadius: BorderRadius.circular(20),
            ),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
          ),
        ],
      ),
    );
  }
}

/// The pine summary at a page's top: a label and its tag, the amount
/// large, a line, and its button.
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
    final soft = white.withValues(alpha: 0.78);
    return Semantics(
      container: true,
      label: semanticsLabel,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 18, 20, 18),
        decoration: BoxDecoration(
          color: t.brand,
          borderRadius: BorderRadius.circular(26),
          boxShadow: [BoxShadow(color: t.brand.withValues(alpha: 0.16), blurRadius: 18, offset: const Offset(0, 6))],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(label, style: tkText(size: 14, weight: 600, color: soft)),
                ),
                if (tag != null)
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
                    decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(999)),
                    child: Text(tag!, style: tkText(size: 12, weight: 700, color: t.brandDarkText)),
                  ),
              ],
            ),
            const SizedBox(height: 4),
            Text(amount, style: tkText(size: 36, weight: 700, lineHeight: 1.15, letterSpacing: -1, color: white)),
            if (sub != null) ...[const SizedBox(height: 4), Text(sub!, style: tkText(size: 13.5, color: soft))],
            if (action != null) ...[const SizedBox(height: 14), action!],
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
