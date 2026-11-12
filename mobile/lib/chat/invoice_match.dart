// Matching money to invoices (web Invoices.tsx InvoiceMatch, WaitingCard,
// HandSplit, SplitForm, CustomerMatching, GiveAccount; ChatAlerts.tsx
// MatchSheet; lib/invoices.ts): money that came in without the reference
// Taskiem gave, and the invoices or orders it may be for; matched, turned
// down, split; an invoice's customer with their credit, account numbers
// and payers remembered; the alerts' Match it.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/forms.dart' show CloseSoon, errorWords, nairaKobo, nairaText;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow, TkSearchBox;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'invoices_page.dart' show fmtTime, openInvoice;
import 'money_sheets.dart' show dateLabel, invoiceStatus;
import 'storefront_orders_page.dart' show openStorefrontOrder;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
int? _intOrNull(Object? v) => (v as num?)?.toInt();
String _str(Object? v) => v as String? ?? '';

const suggestionsPath = '/v1/money/suggestions';
const unmatchedPath = '/v1/money/unmatched';
const issuersPath = '/v1/customer-accounts/issuers';
String suggestionPath(String id, String action) => '/v1/money/suggestions/${Uri.encodeComponent(id)}/$action';
String entryMatchPath(String entry) => '/v1/money/entries/${Uri.encodeComponent(entry)}/match';

/// invoices.ts toKobo for a typed part: null when empty, -1 when not an
/// amount (no part is ever -1 kobo: a minus sign is not an amount).
int? _part(String s) => s.trim().isEmpty ? null : (nairaKobo(s) ?? -1);

/// invoices.ts scoreWords: 85 and up strong, 60 and up likely.
({String label, CardTone tone}) scoreWords(int score) => score >= 85
    ? (label: 'Strong', tone: CardTone.lime)
    : score >= 60
    ? (label: 'Likely', tone: CardTone.plain)
    : (label: 'Possible', tone: CardTone.plain);

/// invoices.ts kindWords.
String kindWords(String kind) => switch (kind) {
  'payment' => 'Payment',
  'account_credit' => 'Transfer into an account',
  'pos_sale' => 'POS sale',
  'statement' => 'Line of a bank statement',
  'cash' => 'Cash',
  _ => kind.replaceAll('_', ' '),
};

/// invoices.ts channelWords: "paystack/main" is "Paystack (main)".
String channelWords(String channel) {
  if (channel == 'upload') return 'Uploaded statement';
  if (channel == 'cash') return 'Cash';
  final i = channel.indexOf('/');
  final rail = i < 0 ? channel : channel.substring(0, i);
  final name = i < 0 ? '' : channel.substring(i + 1);
  final r = rail.isNotEmpty ? rail[0].toUpperCase() + rail.substring(1) : channel;
  return name.isNotEmpty ? '$r ($name)' : r;
}

/// invoices.ts amountWords.
String amountWords(int? kobo) => kobo == null ? 'Amount not known' : naira(kobo);

/// A payment waiting, with what it may be for, strongest first.
typedef Waiting = ({
  String entry,
  int? amount,
  int? left,
  String held,
  String channel,
  String reference,
  String received,
  String kind,
  List<Map<String, dynamic>> candidates,
});

Waiting _waiting(Map<String, dynamic> s, List<Map<String, dynamic>> candidates) => (
  entry: _str(s['entry_id']),
  amount: _intOrNull(s['amount_kobo']),
  left: _intOrNull(s['left_kobo']),
  held: _str(s['held_reason']),
  channel: _str(s['channel']),
  reference: _str(s['reference']),
  received: _str(s['received_at']),
  kind: _str(s['kind']),
  candidates: candidates,
);

/// invoices.ts groupSuggestions: one card per payment, newest first.
List<Waiting> groupSuggestions(List<Map<String, dynamic>> list) {
  final order = <String>[];
  final by = <String, List<Map<String, dynamic>>>{};
  final first = <String, Map<String, dynamic>>{};
  for (final s in list) {
    final id = _str(s['entry_id']);
    if (!by.containsKey(id)) {
      order.add(id);
      first[id] = s;
    }
    (by[id] ??= []).add(s);
  }
  return [for (final id in order) _waiting(first[id]!, by[id]!..sort((a, b) => _int(b['score']) - _int(a['score'])))];
}

/// invoices.ts unmatchedPayments.
List<Waiting> unmatchedPayments(List<Map<String, dynamic>> list) => [for (final u in list) _waiting(u, const [])];

/// invoices.ts leftWords.
String leftWords(int? amount, int? left) {
  if (left == null || amount == null || left >= amount) return '';
  return '${naira(left)} of it is left to match; the rest counts towards other invoices or orders.';
}

bool isOrder(Map<String, dynamic> c) => c['target_kind'] == 'order';

/// invoices.ts targetLabel.
String targetLabel(Map<String, dynamic> c) => isOrder(c) ? 'Order ${c['number']}' : _str(c['number']);

/// invoices.ts openInvoices: sent and not yet fully paid.
List<Map<String, dynamic>> openInvoicesOf(List<Map<String, dynamic>> list) =>
    list.where((x) => const {'sent', 'part_paid', 'overdue'}.contains(x['status'])).toList();

/// invoices.ts matchedLine.
String matchedLine(int? amount, String number, [String kind = 'invoice']) => kind == 'order'
    ? '${amountWords(amount)} matched to order $number. If that is wrong, undo it on the order.'
    : '${amountWords(amount)} matched to $number. If that is wrong, undo it on the invoice.';

/// invoices.ts rejectedLine.
String rejectedLine(String number, [String kind = 'invoice']) => '${kind == 'order' ? 'Order $number' : number} will not be suggested for this payment again.';

/// schoolPayments.ts heldMatchLine: a match waiting for a second person.
String? heldMatchLine(Object? v) {
  if (v is! Map || v['code'] != 'needs_approval' || v['request'] is! Map) return null;
  return 'Not matched yet: this money was entered by hand and ${naira(_int((v['request'] as Map)['amount_kobo']))} is at or above the school’s cash threshold, so the match waits for a second person to approve it under Schools, Approvals.';
}

/// One choice of the picker: an invoice (its id) or an order ("order:ref").
typedef MatchChoice = ({String value, String kind, String number, String label, int due});

/// invoices.ts matchChoices: those already suggested left out.
List<MatchChoice> matchChoices(List<Map<String, dynamic>> invoices, List<Map<String, dynamic>> orders, List<Map<String, dynamic>> candidates) => [
  for (final x in invoices)
    if (!candidates.any((c) => !isOrder(c) && c['invoice_id'] == x['id']))
      (
        value: _str(x['id']),
        kind: 'invoice',
        number: _str(x['number']),
        label: '${x['number']} · ${naira(_int(x['due_kobo']))} due · ${_str((x['customer'] as Map?)?['name'])}',
        due: _int(x['due_kobo']),
      ),
  for (final o in orders)
    if (!candidates.any((c) => isOrder(c) && c['number'] == o['number']))
      (
        value: 'order:${o['number']}',
        kind: 'order',
        number: _str(o['number']),
        label: 'Order ${o['number']} · ${naira(_int(o['due_kobo']))} to pay${_str(o['customer']).isNotEmpty ? ' · ${o['customer']}' : ''}',
        due: _int(o['due_kobo']),
      ),
];

/// invoices.ts matchRequest.
Map<String, Object> matchRequest(String value) => value.startsWith('order:') ? {'order': value.substring(6)} : {'invoice': value};

/// invoices.ts pickerWords: about invoices only while no order waits.
({String summary, String label, String hint, String empty, String submit, String none, String missing}) pickerWords(bool orders) => orders
    ? (
        summary: 'Match to another invoice or order…',
        label: 'Invoice or order',
        hint: 'Invoices sent and not yet fully paid, and storefront orders waiting for their payment.',
        empty: 'Choose an invoice or order',
        submit: 'Match to this one',
        none: 'No other invoice or order is waiting for payment.',
        missing: 'Choose the invoice or order this payment is for.',
      )
    : (
        summary: 'Match to another invoice…',
        label: 'Invoice',
        hint: 'Invoices sent and not yet fully paid.',
        empty: 'Choose an invoice',
        submit: 'Match to this invoice',
        none: 'No other invoice is waiting for payment.',
        missing: 'Choose the invoice this payment is for.',
      );

/// A row of a split: its label, what was typed and what it sends.
class SplitPart {
  SplitPart({required this.key, required this.label, this.what = 'invoice', String amount = '', this.picked = true})
    : amount = TextEditingController(text: amount);
  final String key;
  final String label;
  final String what;
  final TextEditingController amount;
  bool picked;
}

/// invoices.ts handProblem: at least one picked, each its part, the parts
/// no more than is left.
String? handProblem(List<({String name, String amount, bool picked})> rows, int left) {
  final picked = rows.where((r) => r.picked).toList();
  if (picked.isEmpty) return 'Tick the invoices or orders this payment is for.';
  var total = 0;
  for (final r in picked) {
    final k = _part(r.amount);
    if (k == null || k <= 0) return 'Give ${r.name} its part as naira, like 20,000.';
    total += k;
  }
  if (total > left) return 'The parts add up to ${naira(total)}, more than the ${naira(left)} left of this payment.';
  return null;
}

/// invoices.ts handRest.
int handRest(List<({String amount, bool picked})> rows, int left) {
  var total = 0;
  for (final r in rows.where((r) => r.picked)) {
    final k = _part(r.amount);
    if (k == -1) return left;
    total += k ?? 0;
  }
  return (left - total).clamp(0, left < 0 ? 0 : left);
}

/// invoices.ts handRestWords.
String handRestWords(int rest) => rest > 0 ? '${naira(rest)} left to match afterwards.' : 'All of the payment counts towards these.';

/// invoices.ts splitTotal: null when one is not an amount.
int? splitTotal(List<String> amounts) {
  var sum = 0;
  for (final a in amounts) {
    final k = _part(a);
    if (k == -1) return null;
    sum += k ?? 0;
  }
  return sum;
}

/// invoices.ts splitProblem.
String? splitProblem(List<String> amounts, int left) {
  final total = splitTotal(amounts);
  if (total == null) return 'Type each amount as naira, like 20,000.';
  if (total <= 0) return 'Give at least one invoice an amount.';
  if (total > left) return 'The parts add up to ${naira(total)}, more than the ${naira(left)} received.';
  return null;
}

/// invoices.ts splitHint.
String splitHint(Map<String, dynamic> split, String number) {
  final whose = _maps(split['parts']).any((x) => x['sibling'] == true) ? 'the family’s' : 'the same customer’s';
  if (split['over'] == true) {
    return 'The payment is more than is due on $number. Offered across $whose open invoices, $number first then oldest first; change the amounts if you know better.';
  }
  return '$number is offered all of it. If this payment is for several of $whose open invoices, give each its part. An invoice can take more of this payment later: a further part adds to the first.';
}

/// invoices.ts splitRowLabel.
String splitRowLabel(Map<String, dynamic> p) => '${p['number']} (${naira(_int(p['due_kobo']))} due${p['sibling'] == true ? ', a sibling’s' : ''})';

/// invoices.ts splitLine: what a split did.
String splitLine(Object? v) {
  final s = (v as Map?)?.cast<String, dynamic>() ?? const {};
  final parts = [
    for (final x in _maps(s['parts']))
      '${_str(x['target']).replaceFirst(RegExp('^invoice:'), '').replaceFirst(RegExp('^order:'), 'order ')} ${naira(_int(x['amount_kobo']))}',
  ];
  var line = 'Matched in parts: ${parts.join(', ')}.';
  if (_int(s['credit_kobo']) > 0) line += ' ${naira(_int(s['credit_kobo']))} is the customer’s credit, counted towards their next invoice.';
  if (_int(s['left_kobo']) > 0) line += ' ${naira(_int(s['left_kobo']))} is left to match.';
  return line;
}

/// invoices.ts creditWords.
String creditWords(int kobo) =>
    kobo > 0 ? '${naira(kobo)}: paid and not yet counted towards an invoice. It counts towards the next invoice sent to this customer.' : 'None';

/// invoices.ts accountWords.
String accountWords(Map<String, dynamic> a) =>
    '${a['account_number']}${_str(a['bank_name']).isNotEmpty ? ' · ${a['bank_name']}' : ''} (${a['channel']}), reference ${a['reference']}';

/// invoices.ts payerWords.
String payerWords(Map<String, dynamic> p) {
  final times = _int(p['times']);
  return '${p['payer']} (${p['kind'] == 'account' ? 'paying account' : 'name'}), paid ${times == 1 ? 'once' : '$times times'}';
}

/// invoices.ts issuerWords.
String issuerWords(Map<String, dynamic> i) {
  final needs = [
    if (i['needs_email'] == true) 'the customer’s email',
    if (i['needs_bvn'] == true) 'their BVN or NIN (typed in, never kept)',
    if (i['needs_account'] == true) 'the wallet the number pays into',
  ];
  final name = i['names_itself'] == true ? '${i['rail']} names the account itself' : 'named for your business and the customer';
  return '${i['rail']} (${i['connection']}): $name${needs.isNotEmpty ? '; needs ${needs.join(', ')}' : ''}.';
}

/// invoices.ts accountProblem.
String? accountProblem({required bool byHand, required Map<String, dynamic>? issuer, required Map<String, String> f}) {
  String v(String k) => (f[k] ?? '').trim();
  if (byHand) {
    if (!RegExp(r'^[a-z0-9_]{1,60}/[A-Za-z0-9_.-]{1,100}$').hasMatch(v('connection'))) return 'Name the connection the account is on, like paystack/main.';
    if (!RegExp(r'^[0-9A-Za-z-]{4,40}$').hasMatch(v('account_number'))) return 'The account number has 4 to 40 letters and digits.';
    if (v('reference').isEmpty) return 'The account’s reference is required: the payments provider’s reference for it, which its payments carry.';
    return null;
  }
  if (issuer == null) return 'Choose the connection to issue the account on.';
  if (issuer['needs_account'] == true && v('account').isEmpty) return '${issuer['rail']} needs the wallet the number pays into.';
  if (issuer['needs_bvn'] == true && !RegExp(r'^\d{11}$').hasMatch(v('bvn')) && !RegExp(r'^\d{11}$').hasMatch(v('nin'))) {
    return '${issuer['rail']} needs the customer’s BVN or NIN (11 digits).';
  }
  return null;
}

/// invoices.ts accountRequest.
Map<String, Object> accountRequest({required bool byHand, required String connection, required Map<String, String> f}) {
  String v(String k) => (f[k] ?? '').trim();
  if (byHand) {
    return {
      'connection': v('connection'),
      'account_number': v('account_number'),
      'reference': v('reference'),
      if (v('bank_name').isNotEmpty) 'bank_name': v('bank_name'),
    };
  }
  return {
    'connection': connection,
    'issue': true,
    if (v('account').isNotEmpty) 'account': v('account'),
    if (v('bvn').isNotEmpty) 'bvn': v('bvn'),
    if (v('nin').isNotEmpty) 'nin': v('nin'),
  };
}

void _push(BuildContext context, Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));

/// Opens To match.
void openInvoiceMatch(BuildContext context, Auth auth) => _push(context, InvoiceMatchPage(auth: auth));

Widget _notice(TkTokens t, String text) => Semantics(
  liveRegion: true,
  container: true,
  child: Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
    child: Text(text, style: tkText(size: 15, color: t.text)),
  ),
);

Widget _problem(TkTokens t, String text) => Semantics(
  liveRegion: true,
  child: Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Text(text, style: tkText(size: 13, weight: 600, color: t.danger)),
  ),
);

/// A part that opens and shuts (`details`).
class _Fold extends StatefulWidget {
  const _Fold({required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  State<_Fold> createState() => _FoldState();
}

class _FoldState extends State<_Fold> {
  bool _open = false;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Align(
        alignment: Alignment.centerLeft,
        child: Semantics(
          expanded: _open,
          child: TkLink(label: '${_open ? '▾' : '▸'} ${widget.title}', onTap: () => setState(() => _open = !_open)),
        ),
      ),
      if (_open) ...[const SizedBox(height: 8), ...widget.children],
    ],
  );
}

// --- To match ---

/// Invoices > To match: money that may be for an invoice or a storefront
/// order, one card per payment; then the money nothing was suggested for.
class InvoiceMatchPage extends StatefulWidget {
  const InvoiceMatchPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<InvoiceMatchPage> createState() => _InvoiceMatchPageState();
}

class _InvoiceMatchPageState extends State<InvoiceMatchPage> with Polling {
  List<Waiting>? _waiting;
  List<Waiting> _unmatched = const [];
  List<Map<String, dynamic>> _orders = const [];
  int _claims = 0;
  List<Map<String, dynamic>> _invoices = const [];
  String? _error;
  String? _leftError;
  String _said = '';

  bool get _manage => widget.auth.can('invoice.manage');

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    final t = widget.auth.transport;
    await Future.wait([
      () async {
        try {
          final r = (await t.send('GET', suggestionsPath) as Map).cast<String, dynamic>();
          if (mounted) setState(() => (_waiting = groupSuggestions(_maps(r['suggestions'])), _error = null));
        } on Object catch (e) {
          if (mounted) setState(() => _error = errorWords(e));
        }
      }(),
      () async {
        try {
          final r = (await t.send('GET', unmatchedPath) as Map).cast<String, dynamic>();
          if (mounted) {
            setState(() {
              _unmatched = unmatchedPayments(_maps(r['payments']));
              _orders = _maps(r['orders']);
              _claims = _maps(r['claims']).length;
              _leftError = null;
            });
          }
        } on Object catch (e) {
          if (mounted) setState(() => _leftError = errorWords(e));
        }
      }(),
      if (_manage)
        () async {
          try {
            final r = (await t.send('GET', '/v1/invoices') as Map).cast<String, dynamic>();
            if (mounted) setState(() => _invoices = _maps(r['invoices']));
          } on Object {
            // No invoices to match by hand; the suggestions still show.
          }
        }(),
    ]);
  }

  void _done(String line) {
    setState(() => _said = line);
    _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final waiting = _waiting;
    final open = openInvoicesOf(_invoices);
    Widget card(Waiting p) => _WaitingCard(key: ValueKey(p.entry), auth: widget.auth, payment: p, manage: _manage, open: open, orders: _orders, onDone: _done);
    return TkPageScreen(
      title: 'To match',
      description:
          'Money that came in without the reference Taskiem gave, and the invoices or storefront orders it may be for. Match it, or say it is not that one.',
      backLabel: 'Back to Invoices',
      onRefresh: _load,
      children: [
        if (_said.isNotEmpty) _notice(t, _said),
        if (_error != null) TkErrorBox(_error!),
        if (waiting == null && _error == null) const TkLoading(rows: 3, label: 'Loading payments to match…'),
        if (waiting != null && waiting.isEmpty && _unmatched.isEmpty && _claims == 0)
          const TkEmpty(
            icon: 'invoices',
            title: 'Nothing to match',
            line: 'Payments into the accounts Taskiem gave are matched to their invoice as they come in.',
          ),
        if (waiting != null)
          for (final p in waiting) card(p),
        if (_leftError != null) TkErrorBox(_leftError!),
        if (_unmatched.isNotEmpty) ...[
          Text('No invoice suggested', style: tkText(size: 18, weight: 700, color: t.text)),
          Text(
            'Money that came in with nothing to suggest, or whose suggestions were all turned down. Match it by hand if you know the invoice or order.',
            style: tkHint(t),
          ),
          for (final p in _unmatched) card(p),
        ],
        if (_claims > 0)
          Text('${_claims == 1 ? 'A family’s claim waits' : '$_claims families’ claims wait'} to be matched under Schools, on the web.', style: tkHint(t)),
        if (!_manage && waiting != null && waiting.isNotEmpty) Text('You can see these. Someone who manages invoices can match them.', style: tkHint(t)),
      ],
    );
  }
}

class _WaitingCard extends StatefulWidget {
  const _WaitingCard({
    super.key,
    required this.auth,
    required this.payment,
    required this.manage,
    required this.open,
    required this.orders,
    required this.onDone,
  });
  final Auth auth;
  final Waiting payment;
  final bool manage;
  final List<Map<String, dynamic>> open;
  final List<Map<String, dynamic>> orders;
  final void Function(String line) onDone;

  @override
  State<_WaitingCard> createState() => _WaitingCardState();
}

class _WaitingCardState extends State<_WaitingCard> {
  bool _busy = false;
  String? _error;
  String _other = '';
  bool _otherTried = false;

  Waiting get _p => widget.payment;

  Future<void> _run(Future<String> Function() f) async {
    setState(() => (_busy = true, _error = null));
    try {
      final line = await f();
      if (mounted) setState(() => _busy = false);
      widget.onDone(line);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  void _decide(Map<String, dynamic> c, String action) => _run(() async {
    final r = await widget.auth.transport.send('POST', suggestionPath(_str(c['id']), action), body: const {});
    final held = heldMatchLine(r);
    if (held != null) return held;
    final kind = isOrder(c) ? 'order' : 'invoice';
    return action == 'confirm' ? matchedLine(_p.amount, _str(c['number']), kind) : rejectedLine(_str(c['number']), kind);
  });

  void _openTarget(Map<String, dynamic> c) =>
      isOrder(c) ? openStorefrontOrder(context, widget.auth, _str(c['number'])) : openInvoice(context, widget.auth, _str(c['number']));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final choices = matchChoices(widget.open, widget.orders, _p.candidates);
    final words = pickerWords(widget.orders.isNotEmpty);
    final chosen = choices.where((x) => x.value == _other).firstOrNull;
    final left = leftWords(_p.amount, _p.left);
    return TkPageCard(
      children: [
        Semantics(
          header: true,
          child: Text.rich(
            TextSpan(
              children: [
                TextSpan(
                  text: amountWords(_p.amount),
                  style: tkText(size: 20, weight: 700, color: t.text),
                ),
                TextSpan(text: '  ${kindWords(_p.kind)}', style: tkHint(t)),
              ],
            ),
          ),
        ),
        const SizedBox(height: 8),
        for (final (k, v) in [
          ('Received', fmtTime(_p.received)),
          ('Where', channelWords(_p.channel)),
          ('Reference', _p.reference.isEmpty ? 'None' : _p.reference),
        ])
          MergeSemantics(
            child: Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Text.rich(
                TextSpan(
                  children: [
                    TextSpan(
                      text: '$k  ',
                      style: tkText(size: 13, weight: 600, color: t.muted),
                    ),
                    TextSpan(
                      text: v,
                      style: tkText(size: 15, color: t.text),
                    ),
                  ],
                ),
              ),
            ),
          ),
        if (_p.held.isNotEmpty) _notice(t, 'Held for you: ${_p.held}. Nothing was counted towards an invoice.'),
        if (left.isNotEmpty) Text(left, style: tkHint(t)),
        if (_p.candidates.isNotEmpty) ...[const SizedBox(height: 8), Text('May be for', style: tkText(size: 16, weight: 700, color: t.text))],
        for (final c in _p.candidates)
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Wrap(
                  spacing: 8,
                  runSpacing: 4,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    TkLink(label: targetLabel(c), onTap: () => _openTarget(c)),
                    Semantics(
                      label: 'Score ${c['score']} of 100',
                      child: TkPill(scoreWords(_int(c['score'])).label, tone: scoreWords(_int(c['score'])).tone),
                    ),
                    if (isOrder(c))
                      Text('${naira(_int(c['due_kobo']))} to pay${_str(c['customer']).isNotEmpty ? ' · ${c['customer']}' : ''}', style: tkHint(t)),
                  ],
                ),
                if (((c['reasons'] as List?) ?? const []).isNotEmpty)
                  Semantics(
                    container: true,
                    label: 'Why ${targetLabel(c)}',
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [for (final r in (c['reasons'] as List).whereType<String>()) Text('· $r', style: tkHint(t))],
                    ),
                  ),
                if (widget.manage) ...[
                  const SizedBox(height: 6),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      TkSmallButton(
                        label: 'Match',
                        semantics: 'Match to ${targetLabel(c)}',
                        primary: true,
                        onPressed: _busy ? null : () => _decide(c, 'confirm'),
                      ),
                      TkSmallButton(label: 'Not this one', semantics: 'Not this one: ${targetLabel(c)}', onPressed: _busy ? null : () => _decide(c, 'reject')),
                    ],
                  ),
                ],
                if (widget.manage && c['split'] is Map && _maps((c['split'] as Map)['parts']).isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: _SplitForm(auth: widget.auth, suggestion: c, left: _intOrNull(c['left_kobo']) ?? _p.amount ?? 0, onDone: widget.onDone),
                  ),
              ],
            ),
          ),
        if (widget.manage) ...[
          const SizedBox(height: 12),
          _Fold(
            title: words.summary,
            children: choices.isEmpty
                ? [Text(words.none, style: tkHint(t))]
                : [
                    TkKitField(
                      field: false,
                      label: words.label,
                      hint: words.hint,
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: TkSmallButton(
                          label: chosen?.label ?? words.empty,
                          semantics: '${words.label}: ${chosen?.label ?? 'none chosen'}',
                          onPressed: () async {
                            final pick = await showTkModalChoices(
                              context,
                              title: words.label,
                              value: _other,
                              choices: [for (final x in choices) (x.value, x.label)],
                            );
                            if (pick != null) setState(() => _other = pick);
                          },
                        ),
                      ),
                    ),
                    const SizedBox(height: 8),
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TkSmallButton(
                        label: words.submit,
                        onPressed: _busy
                            ? null
                            : () {
                                setState(() => _otherTried = true);
                                if (_other.isEmpty) return;
                                _run(() async {
                                  final r = await widget.auth.transport.send('POST', entryMatchPath(_p.entry), body: matchRequest(_other));
                                  return heldMatchLine(r) ?? matchedLine(_p.amount, chosen?.number ?? _other, chosen?.kind ?? 'invoice');
                                });
                              },
                      ),
                    ),
                    if (_otherTried && _other.isEmpty) _problem(t, words.missing),
                    const SizedBox(height: 12),
                    _HandSplit(auth: widget.auth, payment: _p, choices: matchChoices(widget.open, widget.orders, const []), onDone: widget.onDone),
                  ],
          ),
        ],
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
      ],
    );
  }
}

/// Split a payment by hand: pick invoices and orders, give each its part.
class _HandSplit extends StatefulWidget {
  const _HandSplit({required this.auth, required this.payment, required this.choices, required this.onDone});
  final Auth auth;
  final Waiting payment;
  final List<MatchChoice> choices;
  final void Function(String line) onDone;

  @override
  State<_HandSplit> createState() => _HandSplitState();
}

class _HandSplitState extends State<_HandSplit> {
  late final List<SplitPart> _rows = [
    for (final c in widget.choices) SplitPart(key: c.value, label: c.label, what: c.kind == 'order' ? 'order ${c.number}' : c.number, picked: false),
  ];
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final r in _rows) {
      r.amount.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final r in _rows) {
      r.amount.dispose();
    }
    super.dispose();
  }

  int get _left => widget.payment.left ?? widget.payment.amount ?? 0;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = handProblem([for (final r in _rows) (name: r.what, amount: r.amount.text, picked: r.picked)], _left);
    final rest = handRest([for (final r in _rows) (amount: r.amount.text, picked: r.picked)], _left);
    return _Fold(
      title: 'Split by hand across several…',
      children: [
        Text(
          'Tick each invoice or order this payment is for and give it its part. The parts can add up to at most what is left of the payment; what they leave waits here to be matched. Each part is a match of its own, and can be undone on its own.',
          style: tkHint(t),
        ),
        for (final r in _rows) ...[
          TkChoiceRow(
            control: TkCheckbox(checked: r.picked),
            label: Text(r.label, style: tkText(size: 15, color: t.text)),
            onTap: () => setState(() => r.picked = !r.picked),
          ),
          if (r.picked)
            Padding(
              padding: const EdgeInsets.only(left: 36, bottom: 8),
              child: TkKitField(
                label: 'Part for ${r.what}',
                child: TkTextBox(controller: r.amount, money: true),
              ),
            ),
        ],
        Semantics(liveRegion: true, child: Text(handRestWords(rest), style: tkHint(t))),
        if (_tried && problem != null) _problem(t, problem),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(
            label: 'Match these parts',
            primary: true,
            onPressed: _busy
                ? null
                : () async {
                    setState(() => _tried = true);
                    if (problem != null) return;
                    setState(() => (_busy = true, _error = null));
                    try {
                      final r = await widget.auth.transport.send(
                        'POST',
                        entryMatchPath(widget.payment.entry),
                        body: {
                          'parts': [
                            for (final x in _rows.where((x) => x.picked)) {...matchRequest(x.key), 'amount_kobo': nairaKobo(x.amount.text) ?? 0},
                          ],
                        },
                      );
                      if (mounted) setState(() => _busy = false);
                      widget.onDone(heldMatchLine(r) ?? splitLine(r));
                    } on Object catch (e) {
                      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                    }
                  },
          ),
        ),
      ],
    );
  }
}

/// Split a payment across the customer's open invoices, as offered.
class _SplitForm extends StatefulWidget {
  const _SplitForm({required this.auth, required this.suggestion, required this.left, required this.onDone});
  final Auth auth;
  final Map<String, dynamic> suggestion;
  final int left;
  final void Function(String line) onDone;

  @override
  State<_SplitForm> createState() => _SplitFormState();
}

class _SplitFormState extends State<_SplitForm> {
  late final Map<String, dynamic> _split = (widget.suggestion['split'] as Map).cast<String, dynamic>();
  late final List<SplitPart> _rows = [
    for (final p in _maps(_split['parts']))
      SplitPart(key: _str(p['number']), label: splitRowLabel(p), amount: _int(p['amount_kobo']) > 0 ? nairaText(_int(p['amount_kobo'])) : ''),
  ];
  bool _creditRest = true;
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final r in _rows) {
      r.amount.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final r in _rows) {
      r.amount.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final amounts = [for (final r in _rows) r.amount.text];
    final problem = splitProblem(amounts, widget.left);
    final total = splitTotal(amounts);
    final rest = total == null ? 0 : (widget.left - total).clamp(0, widget.left < 0 ? 0 : widget.left);
    final number = _str(widget.suggestion['number']);
    return _Fold(
      title: 'Split across the customer’s invoices…',
      children: [
        Text('${splitHint(_split, number)} Each part is a match of its own, and can be undone on its own.', style: tkHint(t)),
        const SizedBox(height: 8),
        for (final r in _rows)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: TkKitField(
              label: r.label,
              child: TkTextBox(controller: r.amount, money: true),
            ),
          ),
        Semantics(
          liveRegion: true,
          child: Text(rest > 0 ? '${naira(rest)} not counted towards these invoices.' : 'All of the payment counts towards these invoices.', style: tkHint(t)),
        ),
        if (rest > 0)
          TkChoiceRow(
            control: TkCheckbox(checked: _creditRest),
            label: Text(
              'Keep the rest as the customer’s credit, for their next invoice (otherwise it waits here to be matched)',
              style: tkText(size: 15, color: t.text),
            ),
            onTap: () => setState(() => _creditRest = !_creditRest),
          ),
        if (_tried && problem != null) _problem(t, problem),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(
            label: 'Match in parts',
            semantics: 'Match $number in parts',
            primary: true,
            onPressed: _busy
                ? null
                : () async {
                    setState(() => _tried = true);
                    if (problem != null) return;
                    setState(() => (_busy = true, _error = null));
                    try {
                      final r = await widget.auth.transport.send(
                        'POST',
                        suggestionPath(_str(widget.suggestion['id']), 'confirm'),
                        body: {
                          'parts': [
                            for (final x in _rows)
                              if ((_part(x.amount.text) ?? 0) > 0) {'invoice': x.key, 'amount_kobo': _part(x.amount.text)},
                          ],
                          'credit_rest': _creditRest && rest > 0,
                        },
                      );
                      if (mounted) setState(() => _busy = false);
                      widget.onDone(heldMatchLine(r) ?? splitLine(r));
                    } on Object catch (e) {
                      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                    }
                  },
          ),
        ),
      ],
    );
  }
}

// --- an invoice's customer ---

/// The invoice's customer in matching: what they owe, their credit, their
/// own account numbers and the payers remembered for them.
class CustomerMatching extends StatefulWidget {
  const CustomerMatching({super.key, required this.auth, required this.invoice});
  final Auth auth;
  final Map<String, dynamic> invoice;

  @override
  State<CustomerMatching> createState() => _CustomerMatchingState();
}

class _CustomerMatchingState extends State<CustomerMatching> {
  Map<String, dynamic>? _view;
  String? _error;
  String? _forgetError;
  bool _busy = false;

  String get _id => _str(widget.invoice['id']);

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(CustomerMatching old) {
    super.didUpdateWidget(old);
    // A payment counted: the credit and what is owed, again.
    if (old.invoice['paid_kobo'] != widget.invoice['paid_kobo'] || old.invoice['id'] != widget.invoice['id']) _load();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/invoices/${Uri.encodeComponent(_id)}/customer') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_view = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _view;
    final manage = widget.auth.can('invoice.manage');
    final open = v == null ? const <Map<String, dynamic>>[] : _maps(v['open']);
    final accounts = v == null ? const <Map<String, dynamic>>[] : _maps(v['accounts']);
    final payers = v == null ? const <Map<String, dynamic>>[] : _maps(v['payers']);
    Widget label(String s) => Padding(
      padding: const EdgeInsets.only(top: 10, bottom: 4),
      child: Text(s, style: tkText(size: 13, weight: 600, color: t.muted)),
    );
    return TkPageCard(
      title: 'The customer’s money',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (v == null && _error == null) const TkLoading(rows: 2, label: 'Loading the customer’s credit…'),
        if (v != null) ...[
          if (open.isNotEmpty) ...[
            Text('What they owe', style: tkText(size: 16, weight: 700, color: t.text)),
            Text(
              '${naira(_int(v['owed_kobo']))} in all, on ${open.length} ${open.length == 1 ? 'bill' : 'bills'}. Their money pays the oldest first.',
              style: tkHint(t),
            ),
            for (final o in open)
              TkRow(
                name: o['kind'] == 'credit' ? '${o['number']} · notebook debt' : _str(o['number']),
                icon: 'invoices',
                tone: o['status'] == 'overdue' ? CardTone.danger : null,
                sub: '${naira(_int(o['due_kobo']))} · ${_str(o['due_on']).isNotEmpty ? 'due ${dateLabel(_str(o['due_on']))}' : 'no due date'}',
                pill: TkPill(invoiceStatus(_str(o['status'])).label, tone: invoiceStatus(_str(o['status'])).tone),
                onTap: () => openInvoice(context, widget.auth, _str(o['number'])),
              ),
          ],
          label('Credit'),
          Text(creditWords(_int(v['credit_kobo'])), style: tkText(size: 15, color: t.text)),
          label('Account numbers'),
          if (accounts.isEmpty)
            Text('None. Money into an account number of the customer’s own is matched to their oldest open invoice first.', style: tkHint(t))
          else
            for (final a in accounts) Text(accountWords(a), style: tkText(size: 15, color: t.text)),
          if (manage) _GiveAccount(auth: widget.auth, invoice: _id, onDone: _load),
          label('Payers remembered'),
          if (payers.isEmpty)
            Text(
              'None yet. When someone pays for this customer and you match it, Taskiem remembers the payer and suggests this customer first next time.',
              style: tkHint(t),
            )
          else
            for (final p in payers)
              Padding(
                padding: const EdgeInsets.only(bottom: 6),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 4,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text(payerWords(p), style: tkText(size: 15, color: t.text)),
                    if (manage)
                      TkSmallButton(
                        label: 'Forget this payer',
                        semantics: 'Forget this payer: ${p['payer']}',
                        onPressed: _busy
                            ? null
                            : () async {
                                setState(() => (_busy = true, _forgetError = null));
                                try {
                                  await widget.auth.transport.send('DELETE', '/v1/money/payers/${Uri.encodeComponent(_str(p['id']))}');
                                  if (mounted) showTkToast(this.context, 'Payer forgotten');
                                  await _load();
                                } on Object catch (e) {
                                  if (mounted) setState(() => _forgetError = errorWords(e));
                                }
                                if (mounted) setState(() => _busy = false);
                              },
                      ),
                  ],
                ),
              ),
          if (_forgetError != null) TkErrorBox(_forgetError!),
        ],
      ],
    );
  }
}

/// Give this customer an account number: issued on a connection that can,
/// or one the business already has for them, typed in.
class _GiveAccount extends StatefulWidget {
  const _GiveAccount({required this.auth, required this.invoice, required this.onDone});
  final Auth auth;
  final String invoice;
  final Future<void> Function() onDone;

  @override
  State<_GiveAccount> createState() => _GiveAccountState();
}

class _GiveAccountState extends State<_GiveAccount> {
  bool _open = false;
  List<Map<String, dynamic>>? _issuers;
  String? _issuersError;
  bool _byHand = false;
  String _connection = '';
  final _boxes = {
    for (final k in const ['connection', 'account_number', 'reference', 'bank_name', 'account', 'bvn', 'nin']) k: TextEditingController(),
  };
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in _boxes.values) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in _boxes.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _start() async {
    setState(() => (_open = true, _issuers = null, _issuersError = null));
    try {
      final r = (await widget.auth.transport.send('GET', issuersPath) as Map).cast<String, dynamic>();
      final list = _maps(r['issuers']);
      if (mounted) setState(() => (_issuers = list, _byHand = list.isEmpty, _connection = list.isEmpty ? '' : _str(list.first['connection'])));
    } on Object catch (e) {
      if (mounted) setState(() => _issuersError = errorWords(e));
    }
  }

  void _shut() => setState(() {
    _open = false;
    _tried = false;
    _error = null;
    for (final c in _boxes.values) {
      c.clear();
    }
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (!_open) {
      return Padding(
        padding: const EdgeInsets.only(top: 8),
        child: Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(label: 'Give this customer an account number', onPressed: _start),
        ),
      );
    }
    final list = _issuers;
    final issuer = _byHand ? null : list?.where((i) => i['connection'] == _connection).firstOrNull;
    final f = {for (final e in _boxes.entries) e.key: e.value.text};
    final problem = list == null ? null : accountProblem(byHand: _byHand, issuer: issuer, f: f);
    Widget box(String key, String label, {String? hint, bool digits = false, String? placeholder}) => Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: TkKitField(
        label: label,
        hint: hint,
        child: TkTextBox(controller: _boxes[key]!, plain: !digits, digits: digits ? 40 : null, hint: placeholder),
      ),
    );
    return Semantics(
      container: true,
      label: 'Give this customer an account number',
      explicitChildNodes: true,
      child: Container(
        margin: const EdgeInsets.only(top: 10),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (list == null && _issuersError == null) const TkLoading(rows: 1, label: 'Looking for connections that can issue account numbers…'),
            if (_issuersError != null) TkErrorBox(_issuersError!),
            if (list != null) ...[
              if (list.isEmpty)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Text(
                    'None of your connections can issue account numbers; enter one your payments provider gave you for this customer.',
                    style: tkHint(t),
                  ),
                ),
              if (list.isNotEmpty) ...[
                TkKitField(
                  field: false,
                  label: 'Where from',
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: _byHand ? 'Enter one by hand' : 'Issue one with ${issuer?['rail'] ?? ''} ($_connection)',
                      semantics: 'Where from: ${_byHand ? 'Enter one by hand' : 'Issue one with ${issuer?['rail'] ?? ''} ($_connection)'}',
                      onPressed: () async {
                        final pick = await showTkModalChoices(
                          context,
                          title: 'Where from',
                          value: _byHand ? 'by_hand' : _connection,
                          choices: [
                            for (final i in list) (_str(i['connection']), 'Issue one with ${i['rail']} (${i['connection']})'),
                            ('by_hand', 'Enter one by hand'),
                          ],
                        );
                        if (pick == null) return;
                        setState(() {
                          _byHand = pick == 'by_hand';
                          _connection = _byHand ? '' : pick;
                        });
                      },
                    ),
                  ),
                ),
                const SizedBox(height: 10),
              ],
              if (issuer != null)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Text(issuerWords(issuer), style: tkHint(t)),
                ),
              if (issuer?['needs_account'] == true) box('account', 'Wallet the number pays into'),
              if (issuer?['needs_bvn'] == true) ...[
                box('bvn', 'The customer’s BVN', hint: '11 digits; sent to the provider, never kept.', digits: true),
                box('nin', 'Or their NIN', digits: true),
              ],
              if (_byHand) ...[
                box(
                  'connection',
                  'Connection',
                  hint: 'The connection the account is on, as the money log names it: paystack/main.',
                  placeholder: 'paystack/main',
                ),
                box('account_number', 'Account number'),
                box('reference', 'Reference', hint: 'The provider’s reference for the account, which its payments carry.'),
                box('bank_name', 'Bank'),
              ],
              if (_tried && problem != null) _notice(t, problem),
              if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  TkSmallButton(
                    label: _busy ? 'Registering…' : (_byHand ? 'Register this account' : 'Issue the account number'),
                    primary: true,
                    onPressed: _busy
                        ? null
                        : () async {
                            setState(() => _tried = true);
                            if (problem != null) return;
                            setState(() => (_busy = true, _error = null));
                            try {
                              await widget.auth.transport.send(
                                'POST',
                                '/v1/invoices/${Uri.encodeComponent(widget.invoice)}/customer-accounts',
                                body: accountRequest(byHand: _byHand, connection: _connection, f: f),
                              );
                              if (mounted) {
                                showTkToast(this.context, 'Account number registered');
                                _shut();
                              }
                              await widget.onDone();
                            } on Object catch (e) {
                              if (mounted) setState(() => _error = errorWords(e));
                            }
                            if (mounted) setState(() => _busy = false);
                          },
                  ),
                  TkSmallButton(label: 'Cancel', onPressed: _shut),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }
}

// --- the alerts' Match it ---

/// The alert's Match it: what is open, nearest the amount first, searched;
/// one tapped is matched.
Future<void> showMatchSheet(BuildContext context, {required Auth auth, required String entry, int? amount, String line = '', VoidCallback? onDone}) =>
    showTkSheet<void>(context, (_) => MatchSheet(auth: auth, entry: entry, amount: amount, line: line, onDone: onDone));

class MatchSheet extends StatefulWidget {
  const MatchSheet({super.key, required this.auth, required this.entry, this.amount, this.line = '', this.onDone});
  final Auth auth;
  final String entry;
  final int? amount;
  final String line;
  final VoidCallback? onDone;

  @override
  State<MatchSheet> createState() => _MatchSheetState();
}

class _MatchSheetState extends State<MatchSheet> with CloseSoon {
  List<Map<String, dynamic>>? _list;
  String? _error;
  String? _saved;
  bool _busy = false;
  final _search = TextEditingController();

  @override
  void initState() {
    super.initState();
    _search.addListener(() => setState(() {}));
    widget.auth.transport
        .send('GET', '/v1/chat-alerts/match-options')
        .then(
          (r) {
            final a = widget.amount ?? 0;
            final list = _maps((r as Map)['options'])..sort((x, y) => (_int(x['due_kobo']) - a).abs() - (_int(y['due_kobo']) - a).abs());
            if (mounted) setState(() => _list = list);
          },
          onError: (Object e) {
            if (mounted) setState(() => _error = errorWords(e));
          },
        );
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Future<void> _match(Map<String, dynamic> x) async {
    setState(() => (_busy = true, _error = null));
    try {
      final out = await widget.auth.transport.send(
        'POST',
        entryMatchPath(widget.entry),
        body: x['kind'] == 'order' ? {'order': _str(x['number'])} : {'invoice': _str(x['number'])},
      );
      if (!mounted) return;
      // Held for a second person (a school's cash threshold).
      setState(() => _saved = out is Map && out['code'] == 'needs_approval' ? 'Sent for a second person to approve' : 'Matched to ${x['number']}');
      widget.onDone?.call();
      closeSoon();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final q = _search.text.trim().toLowerCase().replaceAll(RegExp(r'[₦,\s]'), '');
    String whole(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : '${kobo / 100}';
    final shown = (_list ?? const <Map<String, dynamic>>[])
        .where(
          (x) =>
              q.isEmpty ||
              _str(x['number']).toLowerCase().contains(q) ||
              _str(x['customer']).toLowerCase().contains(q) ||
              whole(_int(x['due_kobo'])).contains(q),
        )
        .toList();
    return TkSheet(
      title: widget.amount != null ? 'Match ${naira(widget.amount!)}' : 'Match the payment',
      overlay: _saved == null ? null : TkSavedTick(words: _saved!),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
            child: Text('${widget.line.isNotEmpty ? '${widget.line}. ' : ''}What does it pay?', style: tkText(size: 15, color: t.text)),
          ),
          TkSearchBox(controller: _search, hint: 'Number, amount or name', margin: const EdgeInsets.only(bottom: 12)),
          if (_error != null) TkErrorBox(_error!),
          if (_list == null && _error == null) const TkLoading(rows: 3, label: 'Loading what is open…'),
          if (_list != null && shown.isEmpty) TkEmpty(icon: 'invoices', title: q.isNotEmpty ? 'Nothing open like that' : 'Nothing open to match'),
          for (final x in shown)
            TkRow(
              name: '${x['kind'] == 'order' ? 'Order ' : ''}${x['number']}',
              icon: x['kind'] == 'order' ? 'orders' : 'invoices',
              sub: '${x['customer']} · ${naira(_int(x['due_kobo']))} to pay',
              pill: widget.amount != null && _int(x['due_kobo']) == widget.amount ? const TkPill('Same amount', tone: CardTone.lime) : null,
              onTap: _busy ? null : () => _match(x),
            ),
        ],
      ),
    );
  }
}
