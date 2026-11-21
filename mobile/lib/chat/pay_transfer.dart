// Pay a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md). Pay on a bill opens a small sheet of
// the ways it can be paid (by transfer first, then from Taskiem, then their
// checkout; the way last used with that seller comes first, and with one
// way Pay goes straight to it). Paying by transfer gives an account number
// for that bill: the transfer card shows the bank, the number and the name,
// each with Copy, how much to send exactly, how long the number is good
// for, and follows the payment by itself (asked again every few seconds)
// until it is paid. A customer's invoice in their chat with a business is
// paid the same way.
//
// Words never shown: "virtual account", "dynamic", "rail", "reference".

import 'dart:async';

import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart' show openInBrowser;
import '../app/prefs.dart';
import '../app/sound.dart' show chime;
import '../kit/forms.dart' show errorWords;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/look.dart' show TkSquare;
import '../kit/mobile.dart' show TkHeadButton, TkListRow, TkSection;
import '../kit/modal.dart' show openOrSay, showTkToast;
import '../kit/motion.dart' show TkPulse;
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardActionRows, CardButton, CardPill;
import 'words.dart' show CardTone, clock, naira;

/// An account number to pay into, as the card shows it: a bill's or a
/// customer's invoice's.
class TransferNow {
  const TransferNow({
    required this.state,
    this.bank = '',
    this.number = '',
    this.name = '',
    this.amountKobo = 0,
    this.receivedKobo = 0,
    this.leftKobo = 0,
    this.expiresAt,
    this.problem = '',
    this.next = '',
    this.paidAt,
  });

  /// none (nothing asked yet), asking, open, part, paid, expired or failed.
  final String state;
  final String bank, number, name;

  /// What to send, exactly.
  final int amountKobo;

  /// Part paid: what arrived, and what is left.
  final int receivedKobo, leftKobo;
  final DateTime? expiresAt;

  /// Couldn't get one: why, in plain words.
  final String problem;

  /// Paid: the same seller's next bill still to pay (its id).
  final String next;

  /// Paid: when (as near as it is known).
  final DateTime? paidAt;

  /// A bill's (GET or POST /v1/bills/{id}/transfer).
  factory TransferNow.ofBill(Bill b) {
    final t = b.transfer;
    if (t == null) return TransferNow(state: 'none', leftKobo: b.dueKobo);
    final amount = t.amountKobo ?? 0;
    final received = t.receivedKobo ?? 0;
    return TransferNow(
      state: t.state,
      bank: t.bankName ?? '',
      number: t.accountNumber ?? '',
      name: t.accountName ?? '',
      amountKobo: amount,
      receivedKobo: received,
      leftKobo: t.state == 'part' ? (b.dueKobo > 0 ? b.dueKobo : amount - received) : b.dueKobo,
      expiresAt: t.expiresAt,
      problem: t.problem ?? '',
      next: t.next ?? '',
      // The bill is brought up to date when the payment is counted.
      paidAt: t.state == 'paid' ? (t.paidAt ?? b.mirroredAt) : null,
    );
  }

  /// A customer's invoice's (GET or POST /v1/me/chats/{id}/invoices/{invoice}/transfer).
  factory TransferNow.ofInvoice(CustomerTransfer c) {
    final amount = c.amountKobo ?? 0;
    return TransferNow(
      state: c.state,
      bank: c.bankName ?? '',
      number: c.accountNumber ?? '',
      name: c.accountName ?? '',
      amountKobo: amount,
      receivedKobo: c.state == 'part' && amount > c.dueKobo ? amount - c.dueKobo : 0,
      leftKobo: c.dueKobo,
      expiresAt: c.expiresAt,
      paidAt: c.state == 'paid' ? c.paidAt : null,
    );
  }

  /// The state at [now]: an open number past its time has expired,
  /// whatever was last read (the countdown stops at zero).
  String at(DateTime now) => state == 'open' && expiresAt != null && !expiresAt!.isAfter(now) ? 'expired' : state;

  TransferNow paidSeen(DateTime when) => TransferNow(
    state: state,
    bank: bank,
    number: number,
    name: name,
    amountKobo: amountKobo,
    receivedKobo: receivedKobo,
    leftKobo: leftKobo,
    expiresAt: expiresAt,
    problem: problem,
    next: next,
    paidAt: paidAt ?? when,
  );
}

/// A time left as a clock: "29:12", "4:05", "1:59:30".
String clockLeft(Duration left) {
  final s = left.isNegative ? 0 : left.inSeconds;
  final h = s ~/ 3600, m = (s % 3600) ~/ 60, sec = s % 60;
  String two(int n) => n.toString().padLeft(2, '0');
  // An hour is "60:00", as a number good for 60 minutes is said.
  return s > 3600 ? '$h:${two(m)}:${two(sec)}' : '${s ~/ 60}:${two(sec)}';
}

/// How long a number is good for: "Good for 29:12", and in the last five
/// minutes "Expires in 4:05" (warn); "" with no end.
({String words, bool warn}) goodFor(DateTime? until, DateTime now) {
  if (until == null) return (words: '', warn: false);
  final left = until.difference(now);
  if (left <= Duration.zero) return (words: 'Expired', warn: true);
  if (left <= const Duration(minutes: 5)) return (words: 'Expires in ${clockLeft(left)}', warn: true);
  return (words: 'Good for ${clockLeft(left)}', warn: false);
}

/// The card's state as a pill.
({String word, CardTone tone}) transferPill(String state, bool warn) => switch (state) {
  'asking' || 'none' => (word: 'Getting it', tone: CardTone.plain),
  'open' => warn ? (word: 'Expiring', tone: CardTone.warn) : (word: 'Waiting', tone: CardTone.plain),
  'paid' => (word: 'Paid', tone: CardTone.lime),
  'part' => (word: 'Part-paid', tone: CardTone.warn),
  'expired' => (word: 'Expired', tone: CardTone.plain),
  _ => (word: 'No number', tone: CardTone.danger),
};

/// The transfer card: what it shows in each state, and its buttons. Drawn
/// on a card (the chat's, or the sheet's).
class TransferCard extends StatelessWidget {
  const TransferCard({super.key, required this.s, required this.now, required this.number, this.busy = false, this.onAgain, this.onLonger, this.onNext, this.others = const []});

  final TransferNow s;
  final DateTime now;

  /// The bill's or invoice's number (INV-0042), in the kicker.
  final String number;
  final bool busy;

  /// Get a new number (expired), Pay the rest (part paid), Try again (couldn't).
  final VoidCallback? onAgain;

  /// I need longer: a number good for the longest the seller's side gives.
  final VoidCallback? onLonger;

  /// Paid: Pay the next one.
  final VoidCallback? onNext;

  /// Couldn't get one: the other ways to pay (Pay from Taskiem, their checkout).
  final List<(String, VoidCallback)> others;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final state = busy && (s.state == 'none' || s.state == 'failed') ? 'asking' : s.at(now);
    final good = goodFor(s.expiresAt, now);
    final pill = transferPill(state, good.warn);
    final kicker = number.isEmpty ? 'Pay by transfer' : 'Pay by transfer · $number';
    Widget line(String words, {Color? color, double size = 13, double weight = 400}) => Text(
      words,
      style: tkText(size: size, weight: weight, lineHeight: 1.4, color: color ?? t.muted),
    );
    final body = <Widget>[
      Row(
        children: [
          Expanded(
            child: Text(
              kicker.toUpperCase(),
              semanticsLabel: kicker,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted),
            ),
          ),
          const SizedBox(width: 8),
          CardPill(pill.word, pill.tone),
        ],
      ),
      ...switch (state) {
        'asking' || 'none' => [_Waiting(words: 'Getting an account number…')],
        'open' => [
          line('Send exactly'),
          Semantics(
            label: 'Send exactly ${naira(s.amountKobo)}',
            excludeSemantics: true,
            child: Text(naira(s.amountKobo), style: tkDisplay(size: 30, lineHeight: 1.1, letterSpacing: -1, color: t.text)),
          ),
          const SizedBox(height: 6),
          _Account(s: s),
          const SizedBox(height: 4),
          if (good.words.isNotEmpty)
            Semantics(
              liveRegion: good.warn,
              child: Row(
                children: [
                  ExcludeSemantics(child: TkIcon('clock', size: 16, stroke: 2, color: good.warn ? look.orangeInk : t.muted)),
                  const SizedBox(width: 6),
                  Expanded(child: line(good.words, size: 14, weight: 600, color: good.warn ? look.orangeInk : t.text)),
                ],
              ),
            ),
          _Waiting(words: 'Waiting for your transfer'),
          line('We’ll mark it paid here as soon as it lands.'),
          if (onLonger != null)
            Align(
              alignment: Alignment.centerLeft,
              child: TkLink(label: 'I need longer', bold: true, onTap: busy ? null : onLonger),
            ),
        ],
        'paid' => [
          Row(
            children: [
              TkSquare('b-check', color: look.lime, size: 30),
              const SizedBox(width: 10),
              Expanded(
                child: Text(['Paid ${naira(s.amountKobo)}', if (s.paidAt != null) clock(s.paidAt!)].join(' · '), style: tkDisplay(size: 22, lineHeight: 1.2, letterSpacing: -0.5, color: t.text)),
              ),
            ],
          ),
          if (onNext != null) ...[
            const SizedBox(height: 4),
            CardActionRows(
              children: [CardButton(label: 'Pay the next one', primary: true, busy: busy, onTap: onNext)],
            ),
          ],
        ],
        'part' => [
          Text('Received ${naira(s.receivedKobo)} of ${naira(s.amountKobo)}', style: tkDisplay(size: 20, lineHeight: 1.25, letterSpacing: -0.4, color: t.text)),
          line('${naira(s.leftKobo)} left'),
          if (onAgain != null) ...[
            const SizedBox(height: 4),
            CardActionRows(
              children: [CardButton(label: 'Pay the rest', primary: true, busy: busy, semantics: 'Pay the rest, ${naira(s.leftKobo)}', onTap: onAgain)],
            ),
          ],
        ],
        'expired' => [
          Semantics(
            liveRegion: true,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: const EdgeInsets.only(top: 2),
                  child: ExcludeSemantics(child: TkIcon('b-warn', size: 18, stroke: 2, color: look.orangeInk)),
                ),
                const SizedBox(width: 8),
                Expanded(child: line('This number has expired. Don’t send to it.', size: 15, weight: 600, color: t.text)),
              ],
            ),
          ),
          line('A transfer that was already on its way still counts.'),
          if (onAgain != null) ...[
            const SizedBox(height: 4),
            CardActionRows(
              children: [CardButton(label: 'Get a new number', primary: true, busy: busy, onTap: onAgain)],
            ),
          ],
        ],
        _ => [
          Semantics(liveRegion: true, child: line(s.problem.isEmpty ? 'Their bank did not give a number just now. Try again, or pay another way.' : s.problem, size: 15, weight: 600, color: t.text)),
          if (onAgain != null || others.isNotEmpty) ...[
            const SizedBox(height: 4),
            CardActionRows(
              children: [
                for (final (i, (label, tap)) in others.indexed) CardButton(label: label, primary: i == 0, busy: busy, onTap: tap),
                if (onAgain != null) CardButton(label: 'Try again', primary: others.isEmpty, busy: busy, onTap: onAgain),
              ],
            ),
          ],
        ],
      },
    ];
    return Semantics(
      container: true,
      label: kicker,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < body.length; i++) ...[if (i > 0) SizedBox(height: i == 1 ? 8 : 6), body[i]],
        ],
      ),
    );
  }
}

/// A quiet moving dot and its words: waiting, never a spinner over the number.
class _Waiting extends StatelessWidget {
  const _Waiting({required this.words});
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      liveRegion: true,
      child: Row(
        children: [
          SizedBox(
            width: 16,
            height: 16,
            child: Center(
              child: TkPulse(
                color: look.lime.withValues(alpha: 0.5),
                spread: 5,
                period: const Duration(milliseconds: 1800),
                child: Container(
                  width: 8,
                  height: 8,
                  decoration: BoxDecoration(color: look.lime, shape: BoxShape.circle),
                ),
              ),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(words, style: tkText(size: 14, weight: 600, lineHeight: 1.35, color: t.text)),
          ),
        ],
      ),
    );
  }
}

/// The bank, the number and the name, each with Copy (the number copied
/// without its spaces).
class _Account extends StatelessWidget {
  const _Account({required this.s});
  final TransferNow s;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    Widget row(String label, String shown, String copy, String what, {bool big = false}) => Padding(
      padding: const EdgeInsets.fromLTRB(14, 6, 4, 6),
      child: Row(
        children: [
          Expanded(
            child: MergeSemantics(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(label, style: tkText(size: 12, color: t.muted)),
                  Text(
                    shown,
                    semanticsLabel: big ? copy.split('').join(' ') : null,
                    style: big ? tkDisplay(size: 22, lineHeight: 1.2, letterSpacing: 0.6, color: t.text) : tkText(size: 15.5, weight: 600, lineHeight: 1.3, color: t.text),
                  ),
                ],
              ),
            ),
          ),
          TkHeadButton(
            icon: 'copy',
            label: 'Copy the $what',
            onTap: () async {
              await Clipboard.setData(ClipboardData(text: copy));
              if (context.mounted) showTkToast(context, '${what[0].toUpperCase()}${what.substring(1)} copied');
            },
          ),
        ],
      ),
    );
    final rows = [
      if (s.bank.isNotEmpty) row('Bank', s.bank, s.bank, 'bank'),
      row('Account number', spacedNumber(s.number), s.number.replaceAll(RegExp(r'\s'), ''), 'account number', big: true),
      if (s.name.isNotEmpty) row('Account name', s.name, s.name, 'account name'),
    ];
    return Container(
      decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(16)),
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < rows.length; i++) ...[if (i > 0) Container(height: 1, margin: const EdgeInsets.only(left: 14), color: look.line), rows[i]],
        ],
      ),
    );
  }
}

/// A ten-digit account number as it is read out: "0123 456 789".
String spacedNumber(String n) {
  final d = n.replaceAll(RegExp(r'\s'), '');
  return d.length == 10 ? '${d.substring(0, 4)} ${d.substring(4, 7)} ${d.substring(7)}' : d;
}

// --- where the number comes from ---

/// A bill's or a customer's invoice's account number: read, or asked for.
abstract class TransferSource {
  /// The bill's or invoice's number (INV-0042).
  String get number;

  /// Whether "I need longer" is offered.
  bool get longer => true;
  Future<TransferNow> read();
  Future<TransferNow> ask({bool longer = false});
}

/// A bill's (the buyer's side, invoice.manage).
class BillTransferSource extends TransferSource {
  BillTransferSource(this.auth, this.bill, {this.number = ''});
  final Auth auth;
  final String bill;
  @override
  final String number;

  @override
  Future<TransferNow> read() async => TransferNow.ofBill(await auth.api.getBillTransfer(id: bill));

  @override
  Future<TransferNow> ask({bool longer = false}) async => TransferNow.ofBill(
    await auth.api.askBillTransfer(
      id: bill,
      body: AskBillTransferRequest(longer: longer ? true : null),
    ),
  );
}

/// A customer's own invoice, in their chat with the business.
class InvoiceTransferSource extends TransferSource {
  InvoiceTransferSource(this.auth, {required this.chat, required this.invoice, this.number = ''});
  final Auth auth;
  final String chat, invoice;
  @override
  final String number;

  @override
  Future<TransferNow> read() async => TransferNow.ofInvoice(await auth.api.getMyInvoiceTransfer(id: chat, invoice: invoice));

  @override
  Future<TransferNow> ask({bool longer = false}) async => TransferNow.ofInvoice(
    await auth.api.askMyInvoiceTransfer(
      id: chat,
      invoice: invoice,
      body: AskMyInvoiceTransferRequest(longer: longer ? true : null),
    ),
  );
}

/// How often the card asks again: every 2 seconds while the number comes,
/// every 10 while it waits for the transfer, every 30 once it has expired
/// (a late transfer still counts); not once paid or refused.
Duration? pollEvery(String state) => switch (state) {
  'asking' => const Duration(seconds: 2),
  'open' => const Duration(seconds: 10),
  'expired' => const Duration(seconds: 30),
  _ => null,
};

/// The transfer card, kept up to date: asks for a number when [ask] (Pay
/// by transfer), else reads the one there is (the card in the chat).
class TransferLive extends StatefulWidget {
  const TransferLive({super.key, required this.source, this.ask = false, this.now = DateTime.now, this.sound = true, this.onNext, this.others = const []});
  final TransferSource source;
  final bool ask;
  final DateTime Function() now;

  /// The good-moment sound when it turns paid.
  final bool sound;

  /// Paid, with the same seller's next bill: Pay the next one.
  final void Function(String next)? onNext;

  /// Couldn't get one: the other ways to pay.
  final List<(String, VoidCallback)> others;

  @override
  State<TransferLive> createState() => _TransferLiveState();
}

class _TransferLiveState extends State<TransferLive> {
  TransferNow _s = const TransferNow(state: 'none');
  bool _busy = false;
  bool _read = false;
  Timer? _poll;
  Timer? _tick;

  @override
  void initState() {
    super.initState();
    if (widget.ask) {
      unawaited(_ask());
    } else {
      unawaited(_reread());
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    _tick?.cancel();
    super.dispose();
  }

  void _take(TransferNow s) {
    final was = _s.state;
    if (s.state == 'paid') {
      s = s.paidSeen(widget.now());
      if (_read && was != 'paid' && widget.sound) unawaited(chime());
    }
    _s = s;
    _read = true;
  }

  Future<void> _ask({bool longer = false}) async {
    setState(() => _busy = true);
    try {
      final s = await widget.source.ask(longer: longer);
      if (!mounted) return;
      _take(s);
    } on ApiError catch (e) {
      if (!mounted) return;
      _s = TransferNow(state: 'failed', problem: e.message);
    } on Object catch (e) {
      if (!mounted) return;
      _s = TransferNow(state: 'failed', problem: errorWords(e));
    }
    setState(() => _busy = false);
    _schedule();
  }

  Future<void> _reread() async {
    try {
      final s = await widget.source.read();
      if (!mounted) return;
      setState(() => _take(s));
    } on Object {
      // Offline or refused for now: what is shown stays, asked again.
      if (!mounted) return;
      if (!_read) setState(() => _s = const TransferNow(state: 'asking'));
    }
    _schedule();
  }

  void _schedule() {
    if (!mounted) return;
    _poll?.cancel();
    final every = pollEvery(_s.at(widget.now()));
    _poll = every == null ? null : Timer(every, _reread);
    // The countdown, by the second, while the number is open.
    final open = _s.at(widget.now()) == 'open';
    if (open && _tick == null) {
      _tick = Timer.periodic(const Duration(seconds: 1), (_) {
        if (!mounted) return;
        setState(() {});
        if (_s.at(widget.now()) != 'open') {
          _tick?.cancel();
          _tick = null;
          _schedule();
        }
      });
    } else if (!open) {
      _tick?.cancel();
      _tick = null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = _s;
    final state = s.at(widget.now());
    return TransferCard(
      s: s,
      now: widget.now(),
      number: widget.source.number,
      busy: _busy,
      onAgain: state == 'part' || state == 'expired' || state == 'failed' ? () => _ask() : null,
      onLonger: state == 'open' && widget.source.longer ? () => _ask(longer: true) : null,
      onNext: state == 'paid' && s.next.isNotEmpty && widget.onNext != null ? () => widget.onNext!(s.next) : null,
      others: state == 'failed' ? widget.others : const [],
    );
  }
}

/// The transfer card on a sheet of its own, over the chat or the page.
Future<void> showTransferSheet(
  BuildContext context, {
  required TransferSource source,
  required String title,
  DateTime Function() now = DateTime.now,
  bool sound = true,
  void Function(String next)? onNext,
  List<(String, VoidCallback)> others = const [],
}) => showTkSheet<void>(context, (sheet) {
  final look = TkLook.of(sheet);
  return TkAppLook(
    child: TkSheet(
      title: title,
      body: ListView(
        shrinkWrap: true,
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 20),
        children: [
          Container(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
            decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
            child: TransferLive(
              source: source,
              ask: true,
              now: now,
              sound: sound,
              // Each closes the sheet first: the next bill, another way.
              onNext: onNext == null
                  ? null
                  : (next) {
                      Navigator.of(sheet).pop();
                      onNext(next);
                    },
              others: [
                for (final (label, tap) in others)
                  (
                    label,
                    () {
                      Navigator.of(sheet).pop();
                      tap();
                    },
                  ),
              ],
            ),
          ),
        ],
      ),
    ),
  );
});

// --- Pay: the ways ---

/// A way to pay, as the sheet says it.
({String title, String sub, String icon, Color Function(TkLook) square}) payWayWords(String way, {bool customer = false}) => switch (way) {
  'transfer' => (title: 'Pay by transfer', sub: customer ? 'An account number for this invoice' : 'An account number for this bill', icon: 'b-bank', square: (l) => l.teal),
  'taskiem' => (title: 'Pay from Taskiem', sub: 'From your wallet, once approved', icon: 'b-send', square: (l) => l.violet),
  _ => (title: customer ? 'Pay on the invoice’s page' : 'Pay on their checkout', sub: 'By card, on their own page', icon: 'b-card', square: (l) => l.orange),
};

/// The ways in the order shown: the one last used with this seller first,
/// then as the server gave them (by transfer first).
List<String> orderWays(List<String> ways, String? last) => [if (last != null && ways.contains(last)) last, ...ways.where((w) => w != last)];

/// The small sheet of ways to pay; the way picked, or null.
Future<String?> pickPayWay(BuildContext context, {required String title, required List<String> ways, bool customer = false}) => showTkSheet<String>(context, (sheet) {
  final look = TkLook.of(sheet);
  return TkAppLook(
    child: TkSheet(
      title: title,
      body: ListView(
        shrinkWrap: true,
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
        children: [
          TkSection(
            inset: 56,
            children: [
              for (final w in ways)
                Builder(
                  builder: (_) {
                    final x = payWayWords(w, customer: customer);
                    return TkListRow(title: x.title, sub: x.sub, icon: x.icon, square: x.square(look), onTap: () => Navigator.of(sheet).pop(w));
                  },
                ),
            ],
          ),
        ],
      ),
    ),
  );
});

/// Pay on a bill (the chat's card, the bill's page): its ways in a sheet,
/// or straight to the one there is. With none of them (an older bill), the
/// bill's page, which says how to pay. [onPaid] is told once something was
/// sent (Pay from Taskiem).
Future<void> payBill(
  BuildContext context, {
  required Auth auth,
  required String bill,
  Prefs? prefs,
  DateTime Function() now = DateTime.now,
  bool sound = true,
  VoidCallback? onSent,
  void Function(BuildContext context, String id)? onHowToPay,
}) async {
  final Bill b;
  try {
    b = await auth.api.getBillTransfer(id: bill);
  } on ApiError catch (e) {
    // A server without the ways to pay (or a bill it cannot say them for):
    // the bill itself, which says how to pay it.
    if (!context.mounted) return;
    if (e.status == 404 || e.status == 405) {
      onHowToPay?.call(context, bill);
    } else {
      showTkToast(context, e.message, error: true);
    }
    return;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, errorWords(e), error: true);
    return;
  }
  if (!context.mounted) return;
  final ways = orderWays(b.waysToPay ?? const [], prefs?.payWays[b.supplier]);
  if (ways.isEmpty) {
    if (b.dueKobo <= 0) {
      showTkToast(context, 'Nothing is left to pay on ${b.reference}.');
    } else if (b.disputed) {
      showTkToast(context, 'You queried ${b.reference}: pay it once the query is settled.');
    } else {
      onHowToPay?.call(context, b.id);
    }
    return;
  }
  final way = ways.length == 1 ? ways.first : await pickPayWay(context, title: 'Pay ${naira(b.dueKobo)}', ways: ways);
  if (way == null || !context.mounted) return;
  unawaited(prefs?.rememberPayWay(b.supplier, way));
  Future<void> taskiem() async {
    try {
      await auth.transport.send('POST', '/v1/bills/${Uri.encodeComponent(b.id)}/pay', body: const {});
      if (context.mounted) showTkToast(context, 'Sent for approval');
      onSent?.call();
    } on Object catch (e) {
      if (context.mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  Future<void> checkout() => openOrSay(context, () => openInBrowser(Uri.parse(b.payLink ?? '')), '${b.supplierName}’s checkout could not be opened.');
  switch (way) {
    case 'transfer':
      final others = <(String, VoidCallback)>[
        if (ways.contains('taskiem')) ('Pay from Taskiem', () => unawaited(taskiem())),
        if (ways.contains('checkout')) ('Pay on their checkout', () => unawaited(checkout())),
      ];
      await showTransferSheet(
        context,
        source: BillTransferSource(auth, b.id, number: b.reference),
        title: 'Pay ${b.supplierName}',
        now: now,
        sound: sound,
        others: others,
        onNext: (next) {
          if (context.mounted) unawaited(payBill(context, auth: auth, bill: next, prefs: prefs, now: now, sound: sound, onSent: onSent, onHowToPay: onHowToPay));
        },
      );
    case 'taskiem':
      await taskiem();
    case 'checkout':
      await checkout();
  }
}

/// Pay on a customer's own invoice in their chat with a business: by
/// transfer, or on the invoice's page (when it has one).
Future<void> payMyInvoice(
  BuildContext context, {
  required Auth auth,
  required String chat,
  required String invoice,
  required String business,
  String number = '',
  String? url,
  int? dueKobo,
  DateTime Function() now = DateTime.now,
  bool sound = true,
}) async {
  final page = url ?? '';
  final ways = ['transfer', if (page.isNotEmpty) 'checkout'];
  final way = ways.length == 1 ? ways.first : await pickPayWay(context, title: number.isNotEmpty || dueKobo == null ? 'Pay $number' : 'Pay ${naira(dueKobo)}', ways: ways, customer: true);
  if (way == null || !context.mounted) return;
  Future<void> open() => openOrSay(context, () => openInBrowser(Uri.parse(page)), 'The invoice didn’t open. Try again.');
  if (way == 'checkout') return open();
  await showTransferSheet(
    context,
    source: InvoiceTransferSource(auth, chat: chat, invoice: invoice, number: number),
    title: 'Pay $business',
    now: now,
    sound: sound,
    others: [if (page.isNotEmpty) ('Pay on the invoice’s page', () => unawaited(open()))],
  );
}
