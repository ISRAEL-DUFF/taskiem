// A document's card in a conversation (docs/spec/chat-tools.md §3), as the
// web draws it (web/src/pages/Conversation.tsx CardFrame, CardActions;
// kit.tsx DiaryLine; chat.css .tk-chatcard): the kind and reference, the
// state as a pill, what happened, the amount, an order's progress, and its
// buttons. A change we made ourselves is a diary line instead.

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'deal.dart';
import 'words.dart';

/// `.tk-chatcard`: the frame, inside its bubble.
class CardFrame extends StatelessWidget {
  const CardFrame({super.key, required this.card, required this.title, required this.mine, required this.now, this.current, this.view, required this.actions, this.deal});

  final LinkDocCard card;

  /// The document's newest card, when this one is older: its state shows.
  final LinkDocCard? current;
  final String title;
  final bool mine;
  final DateTime now;
  final List<Widget> actions;

  /// The card's own button to its document (View order, View invoice): on a
  /// row of its own under [actions], the whole width (`.tk-chatcard-actions .view`).
  final Widget? view;

  /// A deal's answers (ChatDeal.tsx DealActions), under the buttons.
  final Widget? deal;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final st = cardStatus(current ?? card, now);
    final steps = stepsFor(card);
    final sub = cardSub(card);
    final step = st.step;
    final children = <Widget>[
      // `.tk-chatcard-top`: the kicker, the pill at the end.
      Row(
        children: [
          Expanded(
            child: Text(
              cardKicker(card).toUpperCase(),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted),
            ),
          ),
          const SizedBox(width: 8),
          CardPill(st.word, st.tone),
        ],
      ),
      Text(breakAfterHyphens(title), style: tkText(size: 15, weight: 600, color: t.text)),
      if (sub.isNotEmpty) Text(sub, style: tkText(size: 14, color: mine ? t.chatOutMeta : t.muted)),
      if (card.deal != null) DealHistory(card: card, now: now),
      if (step != null) CardProgress(steps: steps, step: step),
      if (actions.isNotEmpty) CardActionRows(children: actions),
      // `.tk-deal-form { margin-top: 6px }`, on a line of its own.
      if (deal != null) Padding(padding: const EdgeInsets.only(top: 6), child: deal),
      // Last, the whole width.
      ?view,
    ];
    return Semantics(
      container: true,
      label: cardKicker(card),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          // `.tk-chatcard` on --tk-tile; in our own bubble the panel at 70%.
          color: mine ? cssMix(t.panel, const Color(0x00000000), 0.7) : t.tkTile,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 8), children[i]],
          ],
        ),
      ),
    );
  }
}

/// `.tk-chatcard-actions` (flex-wrap, each button `flex: 1` and its word on
/// one line): the buttons share a row, 8 px apart, as wide as each other
/// where they can be; one that does not fit beside the others goes to the
/// next row, rather than its word being cut or broken.
class CardActionRows extends MultiChildRenderObjectWidget {
  const CardActionRows({super.key, required super.children});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderActionRows();
}

class _ActionRowsData extends ContainerBoxParentData<RenderBox> {}

class _RenderActionRows extends RenderBox
    with ContainerRenderObjectMixin<RenderBox, _ActionRowsData>, RenderBoxContainerDefaultsMixin<RenderBox, _ActionRowsData> {
  static const gap = 8.0;

  @override
  void setupParentData(RenderBox child) {
    if (child.parentData is! _ActionRowsData) child.parentData = _ActionRowsData();
  }

  List<RenderBox> get _kids {
    final out = <RenderBox>[];
    var c = firstChild;
    while (c != null) {
      out.add(c);
      c = childAfter(c);
    }
    return out;
  }

  /// The children in rows, each with the width it needs.
  List<List<(RenderBox, double)>> _rows(double width) {
    final rows = <List<(RenderBox, double)>>[];
    var row = <(RenderBox, double)>[];
    var used = 0.0;
    for (final c in _kids) {
      final need = c.getMaxIntrinsicWidth(double.infinity).clamp(0.0, width);
      if (row.isNotEmpty && used + gap + need > width) {
        rows.add(row);
        row = [];
        used = 0;
      }
      used += (row.isEmpty ? 0 : gap) + need;
      row.add((c, need));
    }
    if (row.isNotEmpty) rows.add(row);
    return rows;
  }

  /// `flex: 1` from nothing: as wide as each other, and none narrower than
  /// its word.
  static List<double> _widths(List<(RenderBox, double)> row, double width) {
    final room = width - gap * (row.length - 1);
    final even = room / row.length;
    if (row.every((r) => r.$2 <= even)) return [for (final _ in row) even];
    final extra = (room - row.fold(0.0, (a, r) => a + r.$2)) / row.length;
    return [for (final r in row) r.$2 + extra];
  }

  @override
  void performLayout() {
    final width = constraints.maxWidth;
    var y = 0.0;
    for (final row in _rows(width)) {
      final ws = _widths(row, width);
      var h = 0.0;
      for (var i = 0; i < row.length; i++) {
        row[i].$1.layout(BoxConstraints.tightFor(width: ws[i]), parentUsesSize: true);
        h = h > row[i].$1.size.height ? h : row[i].$1.size.height;
      }
      var x = 0.0;
      for (var i = 0; i < row.length; i++) {
        final c = row[i].$1;
        (c.parentData! as _ActionRowsData).offset = Offset(x, y + (h - c.size.height) / 2);
        x += ws[i] + gap;
      }
      y += h + gap;
    }
    size = constraints.constrain(Size(width, y > 0 ? y - gap : 0));
  }

  @override
  double computeMinIntrinsicWidth(double height) => _kids.fold(0.0, (a, c) => a > c.getMinIntrinsicWidth(height) ? a : c.getMinIntrinsicWidth(height));

  @override
  double computeMaxIntrinsicWidth(double height) {
    final k = _kids;
    return k.fold(0.0, (a, c) => a + c.getMaxIntrinsicWidth(height)) + gap * (k.isEmpty ? 0 : k.length - 1);
  }

  @override
  double computeMinIntrinsicHeight(double width) => computeMaxIntrinsicHeight(width);

  @override
  double computeMaxIntrinsicHeight(double width) {
    var y = 0.0;
    for (final row in _rows(width)) {
      final ws = _widths(row, width);
      var h = 0.0;
      for (var i = 0; i < row.length; i++) {
        final ch = row[i].$1.getMaxIntrinsicHeight(ws[i]);
        h = h > ch ? h : ch;
      }
      y += h + gap;
    }
    return y > 0 ? y - gap : 0;
  }

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) => defaultHitTestChildren(result, position: position);

  @override
  void paint(PaintingContext context, Offset offset) => defaultPaint(context, offset);
}

/// kit.css `.tk-pill`: a state in a word.
class CardPill extends StatelessWidget {
  const CardPill(this.word, this.tone, {super.key});
  final String word;
  final CardTone tone;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final (bg, ink) = switch (tone) {
      CardTone.lime => (t.brandDark, t.brandDarkText),
      CardTone.warn => (t.tkWarnBg, t.tkWarnInk),
      CardTone.danger => (t.tkDangerBg, t.tkDangerInk),
      CardTone.plain => (t.tkSoft, t.text),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
      child: Text(word, maxLines: 1, style: tkText(size: 12, weight: 700, color: ink)),
    );
  }
}

/// A card's button (`.tk-chatcard-actions button.small`): a 40 px pill,
/// on the panel with a line round it, or the primary colour.
class CardButton extends StatelessWidget {
  const CardButton({super.key, required this.label, this.onTap, this.primary = false, this.busy = false, this.semantics});

  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final bool busy;

  /// What a screen reader says (with the document's reference).
  final String? semantics;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final bg = primary ? t.primary : t.panel;
    final line = primary ? t.primary : t.borderStrong;
    final ink = primary ? t.primaryText : (dark ? t.text : t.brand);
    return Semantics(
      button: true,
      enabled: !busy && onTap != null,
      label: semantics ?? label,
      excludeSemantics: true,
      child: TkPress(
        onTap: busy ? null : onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 16),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: bg,
            border: Border.all(color: line),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: ink),
          ),
        ),
      ),
    );
  }
}

/// What a card's buttons do: a step taken from the card, as the web's
/// CardActions takes it, and the document opened.
class CardActs {
  CardActs({required this.auth, required this.onDone, this.onOpen, this.ask, this.said});

  final Auth auth;

  /// After a step: what is newer is asked for.
  final Future<void> Function() onDone;

  /// A document of ours opened in the app (View order, View invoice, Pay).
  final void Function(LinkDocCard card)? onOpen;

  /// Asks first (the web's confirmation): true to go on.
  final Future<bool> Function(String title, String ok)? ask;

  /// Says a step is done (the web's toast).
  final void Function(String words)? said;

  bool busy = false;
  String? error;

  /// A step sent; [done] said once it is.
  Future<void> post(String path, [Map<String, dynamic>? body, String? done]) async {
    busy = true;
    error = null;
    try {
      await auth.transport.send('POST', path, body: body ?? const {});
      if (done != null) said?.call(done);
      // Done: what is newer is asked for, and its own failure is the
      // conversation's to say, not the step's.
      try {
        await onDone();
      } on Object {
        // Asked again on the next turn.
      }
    } on ApiError catch (e) {
      error = e.message;
    } on Offline {
      error = 'You’re offline. Try again when you’re back online.';
    } finally {
      busy = false;
    }
  }
}

/// The card's own steps (Conversation.tsx CardActions): what the person may
/// do next from the card, by its document and event and their permissions.
List<Widget> cardActions(LinkDocCard card, CardActs acts, VoidCallback changed) {
  final can = acts.auth.can;
  final id = Uri.encodeComponent(card.id ?? card.reference);
  final ref = card.reference;
  // Each says what it did, as the web's do; Decline asks first.
  Widget step(String label, String path, String done, {bool primary = true, Map<String, dynamic>? body, String? askFirst}) => CardButton(
    label: label,
    primary: primary,
    busy: acts.busy,
    semantics: '$label $ref',
    onTap: () async {
      if (askFirst != null && !(await acts.ask?.call(askFirst, label) ?? true)) return;
      final f = acts.post(path, body, done);
      changed();
      await f;
      changed();
    },
  );
  if (card.doc == 'delivery' && can('storefront.manage|order.handle')) {
    switch (card.event) {
      case 'offered':
        return [step('Accept', '/v1/trade-orders/$id/accept', 'Accept'), step('Can’t', '/v1/trade-orders/$id/cant', 'Passed on $ref', primary: false)];
      case 'accepted':
        return [step('Picked up', '/v1/trade-orders/$id/picked_up', 'Picked up')];
      case 'picked_up':
        return [step('Arrived', '/v1/trade-orders/$id/arrived', 'Arrived')];
      case 'arrived':
        return [step('Delivered', '/v1/trade-orders/$id/delivered', 'Delivered')];
    }
  }
  if (card.doc == 'dispatch' && card.event == 'delivered' && can('storefront.manage')) {
    final r = Uri.encodeComponent(ref);
    return [
      step('Confirm, pay the fee', '/v1/storefront/orders/$r/confirm', 'Confirmed $ref'),
      step('Paid in cash', '/v1/storefront/orders/$r/confirm', '$ref paid in cash', primary: false, body: {'how': 'cash'}),
    ];
  }
  switch ('${card.doc}:${card.event}') {
    case 'order:ordered':
      if (can('storefront.manage|order.handle')) {
        return [
          step('Accept', '/v1/trade-orders/$id/accept', '$ref accepted'),
          step('Decline', '/v1/trade-orders/$id/decline', '$ref declined', primary: false, askFirst: 'Decline $ref?'),
        ];
      }
    case 'purchase:change':
      if (card.wasKobo == null && can('storefront.manage')) return [step('Accept the change', '/v1/stock/purchases/$id/accept-change', 'Change accepted')];
    case 'purchase:delivered':
      if (can('table.edit')) return [step('All came', '/v1/stock/purchases/$id/all-came', 'Received, and your stock is up')];
    case 'bill:bill':
      if (can('invoice.manage')) {
        return [
          CardButton(
            label: 'Pay${card.amountKobo != null ? ' ${naira(card.amountKobo!)}' : ''}',
            primary: true,
            semantics: 'Pay $ref',
            onTap: acts.onOpen == null ? null : () => acts.onOpen!(card),
          ),
        ];
      }
  }
  return const [];
}

/// The card's own button to its document: View order, View invoice, View delivery.
Widget viewButton(LinkDocCard card, CardActs acts) {
  final label = isDelivery(card)
      ? 'View delivery'
      : card.doc == 'order' || card.doc == 'purchase'
      ? 'View order'
      : 'View invoice';
  return CardButton(label: label, semantics: '$label ${card.reference}', onTap: acts.onOpen == null ? null : () => acts.onOpen!(card));
}

/// kit.tsx DiaryLine: a change we made ourselves, told as a line in a
/// soft pill, with a tick; "and 2 earlier changes" unfolds the rest.
class DiaryLine extends StatelessWidget {
  const DiaryLine({super.key, required this.words, this.sub, required this.when, this.more, this.onTap});

  final String words;
  final String? sub;

  /// "You, 10:42": said, not drawn.
  final String when;
  final Widget? more;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final arrow = words.lastIndexOf(' → ');
    final spans = <InlineSpan>[
      if (arrow >= 0) ...[
        TextSpan(text: words.substring(0, arrow + 3)),
        TextSpan(
          text: words.substring(arrow + 3),
          style: const TextStyle(fontWeight: FontWeight.w700, fontVariations: [FontVariation('wght', 700)]),
        ),
      ] else
        TextSpan(text: words),
      if (sub != null && sub!.isNotEmpty)
        TextSpan(
          text: ' $sub',
          style: TextStyle(color: t.muted),
        ),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: LayoutBuilder(
        builder: (context, box) => Center(
          child: ConstrainedBox(
            // `.tk-diary { max-width: 92% }`
            constraints: BoxConstraints(maxWidth: box.maxWidth * 0.92),
            child: Column(
              children: [
                Semantics(
                  label: when,
                  child: TkPress(
                    onTap: onTap,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                      decoration: BoxDecoration(
                        color: t.panel,
                        borderRadius: BorderRadius.circular(999),
                        boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 2), blurRadius: 8)],
                      ),
                      // `display: inline-flex; flex-wrap: wrap; justify-content:
                      // center; gap: 4px 8px`: words too long to sit beside the
                      // tick put it on a line of its own, above them.
                      child: Wrap(
                        alignment: WrapAlignment.center,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        spacing: 8,
                        runSpacing: 4,
                        children: [
                          TkIcon('tick', size: 16, color: t.ok),
                          Text.rich(TextSpan(children: spans), style: tkText(size: 13, color: t.text)),
                        ],
                      ),
                    ),
                  ),
                ),
                if (more != null) Padding(padding: const EdgeInsets.only(top: 4), child: more),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// chat.css `.tk-progress`: the steps as a bar of [steps] columns, the one
/// it is at in words under it.
class CardProgress extends StatelessWidget {
  const CardProgress({super.key, required this.steps, required this.step});
  final List<String> steps;
  final int step;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      label: '${steps[step]}: step ${step + 1} of ${steps.length}',
      excludeSemantics: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // `.tk-progress-bar`: four columns, 4 px apart, 5 px tall.
          Row(
            children: [
              for (var i = 0; i < steps.length; i++) ...[
                if (i > 0) const SizedBox(width: 4),
                Expanded(
                  child: Container(
                    height: 5,
                    decoration: BoxDecoration(color: i <= step ? (dark ? t.brandDark : t.brand) : t.borderStrong, borderRadius: BorderRadius.circular(3)),
                  ),
                ),
              ],
            ],
          ),
          const SizedBox(height: 6),
          // `.tk-progress-words`: the same columns with no gap.
          Row(
            children: [
              for (var i = 0; i < steps.length; i++)
                Expanded(
                  child: Text(
                    steps[i],
                    style: tkText(size: 11.5, weight: i == step ? 600 : 400, color: i == step ? t.text : t.muted),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}
