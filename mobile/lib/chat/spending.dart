// Spending by label, in Money's reports (docs/spec/what-a-business-buys.md
// §5.4): a period's spending under each label kept, Not labelled apart;
// Bought to sell or make with (lines added to stock) and Bought to keep
// (lines kept track of) apart from running costs; and profit, leaving out
// Bought to keep. A card on Money for its period, and a page by month.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show MoneySpending, MoneySpendingLabelsItem;
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart' show TkCallout, TkEyebrow, TkHighlight, TkHighlights, TkSplitBar, TkSquare, TkTopCard, tkFigure;
import '../kit/mobile.dart' show TkChips, TkListRow, TkRoute, TkSection;
import '../kit/page.dart' show TkPageScreen;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'words.dart' show naira;

String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

const _months = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

/// A label's line: what of it was bought to sell or to keep, and how many
/// entries.
String spendingLine(MoneySpendingLabelsItem l) => [
  if (l.boughtToSellKobo > 0) '${naira(l.boughtToSellKobo)} to sell or make with',
  if (l.boughtToKeepKobo > 0) '${naira(l.boughtToKeepKobo)} to keep',
  if (l.runningCostsKobo > 0 && (l.boughtToSellKobo > 0 || l.boughtToKeepKobo > 0)) '${naira(l.runningCostsKobo)} running',
  l.entries == 1 ? '1 entry' : '${l.entries} entries',
].join(' · ');

/// Running costs, Bought to sell or make with, and Bought to keep as one
/// bar, its amounts in the key (or its shares, where tiles say the
/// amounts).
Widget spendingSplit(BuildContext context, MoneySpending s, {bool amounts = true}) {
  final look = TkLook.of(context);
  final total = s.totalKobo <= 0 ? 1 : s.totalKobo;
  int share(int k) => k <= 0 ? 0 : (100 * k / total).round().clamp(1, 100);
  return TkSplitBar(
    percent: !amounts,
    parts: [
      (label: amounts ? 'Running costs ${naira(s.runningCostsKobo)}' : 'Running', share: share(s.runningCostsKobo), color: look.orange),
      (label: amounts ? 'Bought to sell or make with ${naira(s.boughtToSellKobo)}' : 'To sell', share: share(s.boughtToSellKobo), color: look.teal),
      (label: amounts ? 'Bought to keep ${naira(s.boughtToKeepKobo)}' : 'To keep', share: share(s.boughtToKeepKobo), color: look.violet),
    ],
  );
}

Widget _labelRow(BuildContext context, MoneySpendingLabelsItem l) {
  final none = l.labelId == null;
  return TkListRow(
    title: none ? 'Not labelled' : l.name,
    letters: none ? null : l.name,
    icon: none ? 'b-hash' : null,
    sub: spendingLine(l),
    amount: naira(l.kobo),
    semanticsLabel: '${none ? 'Not labelled' : l.name}: ${naira(l.kobo)}, ${spendingLine(l)}',
  );
}

/// Money's card for its period: the split, and the three labels spent on
/// most, with See all opening the page by month. Nothing when it cannot
/// be read (spending is a help on Money, not its subject).
class SpendingCard extends StatefulWidget {
  const SpendingCard({super.key, required this.auth, required this.from, required this.to, this.now});
  final Auth auth;
  final String from;
  final String to;
  final DateTime Function()? now;

  @override
  State<SpendingCard> createState() => _SpendingCardState();
}

class _SpendingCardState extends State<SpendingCard> {
  MoneySpending? _s;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(SpendingCard old) {
    super.didUpdateWidget(old);
    if (old.from != widget.from || old.to != widget.to) {
      _s = null;
      _load();
    }
  }

  Future<void> _load() async {
    final from = widget.from, to = widget.to;
    try {
      final s = await widget.auth.api.getMoneySpending(from: from, to: to);
      if (mounted && from == widget.from && to == widget.to) setState(() => _s = s);
    } on Object {
      // No card.
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = _s;
    if (s == null) return const SizedBox.shrink();
    return TkSection(
      title: 'Spending by label',
      onSeeAll: () => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => SpendingPage(auth: widget.auth, now: widget.now))),
      seeAllLabel: 'See spending by label, month by month',
      empty: 'Nothing spent in this period.',
      children: [
        if (s.totalKobo > 0) Padding(padding: const EdgeInsets.fromLTRB(16, 14, 16, 12), child: spendingSplit(context, s)),
        for (final l in s.labels.take(3)) _labelRow(context, l),
      ],
    );
  }
}

/// Spending by label, a month at a time: profit leaving out Bought to
/// keep, the three kinds apart, then every label.
class SpendingPage extends StatefulWidget {
  const SpendingPage({super.key, required this.auth, this.now, this.backLabel = 'Back to Money'});
  final Auth auth;
  final DateTime Function()? now;
  final String backLabel;

  @override
  State<SpendingPage> createState() => _SpendingPageState();
}

class _SpendingPageState extends State<SpendingPage> {
  late final List<(String, String)> _choices = () {
    final now = (widget.now ?? DateTime.now)().toUtc().add(const Duration(hours: 1));
    return [
      for (var i = 0; i < 6; i++)
        () {
          final d = DateTime.utc(now.year, now.month - i);
          return ('${d.year}-${d.month.toString().padLeft(2, '0')}', i == 0 ? 'This month' : (d.year == now.year ? _months[d.month - 1] : '${_months[d.month - 1]} ${d.year}'));
        }(),
    ];
  }();
  late String _month = _choices.first.$1;
  MoneySpending? _s;
  String? _error;
  int _asked = 0;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final s = await widget.auth.api.getMoneySpending(month: _month);
      if (mounted && asked == _asked) setState(() => (_s = s, _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = _why(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final s = _s;
    final p = s?.profit;
    return TkPageScreen(
      title: 'Spending by label',
      titleSize: 30,
      backLabel: widget.backLabel,
      description:
          'What the business spent in a month, under its own labels. What went to stock is Bought to sell or make with; what is on Things we have is Bought to keep, and it is left out of profit, since the business still has it.',
      under: TkChips<String>(
        label: 'Month',
        chips: _choices,
        on: _month,
        onPick: (v) {
          setState(() => (_month = v, _s = null));
          _load();
        },
      ),
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (s == null && _error == null) const TkLoading(rows: 3, label: 'Loading the month’s spending…'),
        if (s != null && p != null) ...[
          TkTopCard(
            semanticsLabel: 'Profit: ${naira(p.profitKobo)}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Profit', color: p.profitKobo < 0 ? look.redInk : look.limeInk, icon: 'b-bars', trailing: 'Bought to keep left out'),
                const SizedBox(height: 6),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(p.profitKobo), maxLines: 1, style: tkFigure(context, size: 40))),
                Text(
                  '${naira(p.inKobo)} in, less ${naira(p.costsKobo)} spent${p.feesKobo > 0 ? ' and ${naira(p.feesKobo)} in fees' : ''}',
                  style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
                ),
                if (s.totalKobo > 0) ...[const SizedBox(height: 12), spendingSplit(context, s, amounts: false)],
              ],
            ),
          ),
          TkHighlights(
            tiles: [
              TkHighlight(label: 'Running costs', icon: 'b-cash', color: look.orangeInk, value: naira(s.runningCostsKobo), line: 'in profit'),
              TkHighlight(label: 'To sell or make with', icon: 'b-box', color: look.tealInk, value: naira(s.boughtToSellKobo), line: 'stock bought, in profit'),
              TkHighlight(
                wide: true,
                label: 'Bought to keep',
                icon: 'b-hash',
                color: look.violetInk,
                value: naira(s.boughtToKeepKobo),
                line: 'on Things we have, left out of profit',
              ),
            ],
          ),
          if (s.labels.isEmpty)
            const TkEmpty(icon: 'tag', title: 'Nothing spent this month', line: 'Expenses and paid orders show here under their labels.')
          else
            TkSection(title: 'By label', children: [for (final l in s.labels) _labelRow(context, l)]),
        ],
      ],
    );
  }
}
