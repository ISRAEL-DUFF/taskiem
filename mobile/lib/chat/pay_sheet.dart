// Pay one person (P1; docs/spec/chat-pay-people.md "Pay one person", and
// chat-menus.md M4's forms): the Pay sheet. An action of the chat's
// catalogue with `confirm: "passkey"` (pay.person, pay.account) is not
// sent to Taskiem as typed words: its form prepares the payment (POST
// /v1/payments), then shows what the bank says, large, with who, the bank,
// the account masked, how much and what for, and what to look at first (a
// first payment, an account changed, the same payment again). Pay asks
// for the phone's passkey for this payment alone (money.pay on
// payment:<id>, the single-action step-up approvals use) and confirms it
// (POST /v1/payments/{id}/confirm). Nothing pays on words.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/icon.dart' show TkIcon;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'deal.dart' show dealKobo;
import 'groups.dart' show pickOneOf;
import 'words.dart' show CardTone, naira;

/// What a payment is for: the catalogue's words ("a delivery") and the
/// server's (delivery).
const payPurposes = [('a bill', 'bill'), ('wages', 'wages'), ('a delivery', 'delivery'), ('a refund', 'refund'), ('stock bought', 'stock'), ('other', 'other')];

/// The server's purpose for the words chosen (or typed); other when none.
String purposeOf(String words) {
  final w = words.trim().toLowerCase();
  for (final (said, key) in payPurposes) {
    if (w == said || w == key) return key;
  }
  return 'other';
}

/// What a payment is for, in words: its own words for other.
String purposeWords(Payment p) {
  final note = p.purposeNote ?? '';
  if (p.purpose == 'other' && note.isNotEmpty) return note;
  final w = payPurposes.where((x) => x.$2 == p.purpose).firstOrNull?.$1 ?? p.purpose;
  return w[0].toUpperCase() + w.substring(1);
}

/// A payment's status in words, and its tone.
({String words, CardTone tone}) paymentStatus(String status) => switch (status) {
  'prepared' => (words: 'Not sent yet', tone: CardTone.warn),
  'sending' => (words: 'Sending', tone: CardTone.plain),
  'waiting_ok' => (words: 'Waiting for an OK', tone: CardTone.warn),
  'paid' => (words: 'Paid', tone: CardTone.lime),
  'failed' => (words: 'Not paid', tone: CardTone.danger),
  'rejected' => (words: 'Refused', tone: CardTone.danger),
  'unknown' => (words: 'Sent, waiting for the bank', tone: CardTone.plain),
  'cancelled' => (words: 'Cancelled', tone: CardTone.plain),
  _ => (words: status, tone: CardTone.plain),
};

/// The bank's name for the account once checked, else the payee's.
String paymentName(Payment p) {
  final n = p.accountName;
  return n is String && n.trim().isNotEmpty ? n : p.payee;
}

/// Who and where, under the name: the payee when the bank calls them
/// otherwise, the bank, the account masked.
String paymentWhere(Payment p) {
  final name = paymentName(p);
  return [if (p.payee.isNotEmpty && p.payee.toLowerCase() != name.toLowerCase()) p.payee, p.bankName, p.account].where((x) => x.isNotEmpty).join(' · ');
}

/// Why a payment waits for the owner's OK, in words.
String needsOkWords(String needs) => [
  for (final n in needs.split(','))
    switch (n.trim()) {
      'limit' => 'Over the limit you may pay',
      'changed' => 'The account changed since it was last paid',
      'first' => 'Never paid before',
      _ => '',
    },
].where((w) => w.isNotEmpty).join('; ');

/// What was said when it was sent: at once, or waiting for the owner's OK.
String paymentSentWords(Payment p) => p.status == 'waiting_ok' ? 'Sent for the owner’s OK' : 'Sent to the bank';

/// The prepared payment confirmed: with a code from the person's
/// authenticator when one is typed, else the phone's passkey for this
/// payment (money.pay on `payment:<id>`). [again] says yes to the same
/// payment made in the last day. Without a code the server is asked first,
/// with no proof, so what stops the payment (the same payment again) is
/// said before the passkey is asked for, and the passkey is not wasted.
Future<Payment> confirmPaymentWithProof(Auth auth, Payment p, {bool again = false, String code = ''}) async {
  Future<Payment> send(Map<String, dynamic>? passkey, String? totp) async => (await auth.api.confirmPayment(
    id: p.id,
    body: ConfirmPaymentRequest(passkey: passkey, totp: totp, again: again ? true : null),
  )).payment;
  if (code.isNotEmpty) return send(null, code);
  try {
    return await send(null, null);
  } on ApiError catch (e) {
    if (e.status != 403 || e.body['step_up'] == null) rethrow;
  }
  return send(await passkeyProof(auth, 'money.pay', 'payment:${p.id}'), null);
}

/// Whether the name a message gives is the bank's: every word of one
/// among the other's (the bank often has a middle name more), any order.
bool sameName(String said, String bank) {
  List<String> words(String s) => s.toLowerCase().split(RegExp('[^a-z]+')).where((w) => w.length > 1).toList();
  final a = words(said), b = words(bank);
  if (a.isEmpty || b.isEmpty) return true;
  return a.every(b.contains) || b.every(a.contains);
}

/// The name a message gave against the bank's, in amber, when they differ.
Widget? nameDiffers(BuildContext context, String said, String bank) {
  if (said.trim().isEmpty || bank.trim().isEmpty || sameName(said, bank)) return null;
  final look = TkLook.of(context);
  return TkCallout(
    leading: TkSquare('b-alert', color: look.orange),
    title: 'The message says ${said.trim()}; the bank says ${bank.trim()}.',
    sub: 'Check with them before you pay.',
    subLines: 3,
    color: look.orange,
  );
}

/// The Pay sheet's form for one of the business's riders, paid to the bank
/// account kept for their payouts: how much, what for (a delivery unless
/// changed) and a note.
ChatAction riderPayAction(String name) => ChatAction.fromJson({
  'id': 'pay.rider',
  'title': 'Pay $name',
  'line': 'To the bank account kept for their payouts',
  'icon': 'money',
  'does': 'asks',
  'opens': 'form',
  'tool': 'pay',
  'confirm': 'passkey',
  'fields': [
    {'key': 'amount', 'label': 'How much?', 'kind': 'money'},
    {
      'key': 'for',
      'label': 'What is it for?',
      'kind': 'choice',
      'choices': [for (final (w, _) in payPurposes) w],
    },
    {'key': 'note', 'label': 'A few words for their bank statement?', 'kind': 'text', 'optional': true},
  ],
});

/// The Pay sheet over the chat (an action with `confirm: "passkey"`): its
/// form, then the payment prepared to check and pay. Closes with the
/// payment once it is sent, or null.
class PaySheet extends StatefulWidget {
  const PaySheet({super.key, required this.auth, required this.action, this.initial = const {}, this.from = 'chat', this.riderId, this.said = ''});
  final Auth auth;
  final ChatAction action;
  final Map<String, String> initial;

  /// Where it started (tools, chat, message, card).
  final String from;

  /// One of the business's riders, paid to their kept bank account.
  final String? riderId;

  /// The name the message gave for the account, set against the bank's.
  final String said;

  @override
  State<PaySheet> createState() => _PaySheetState();
}

class _PaySheetState extends State<PaySheet> {
  late final List<ChatField> _fields = widget.action.fields ?? const [];
  late final Map<String, TextEditingController> _boxes = {
    for (final f in _fields)
      if (f.kind != 'bank') f.key: TextEditingController(text: widget.initial[f.key] ?? ''),
  };
  // The bank, as its name: chosen from the list, or as the message said it.
  late final Map<String, String> _banksChosen = {
    for (final f in _fields)
      if (f.kind == 'bank' && (widget.initial[f.key] ?? '').isNotEmpty) f.key: widget.initial[f.key]!,
  };
  Map<String, List<String>> _choices = const {};
  // The banks to pay at, for the number they were read for (the likely
  // ones first).
  List<ListPayBanksResponseBanksItem>? _banks;
  String? _banksFor;
  // What each bank said of the number (by its code), for [_saidFor].
  final Map<String, PayNameCheckResponseBanksItem> _said = {};
  // The banks "Check the likely banks for me" asked, in their order.
  List<String> _likelyAsked = const [];
  String? _saidFor;
  bool _checking = false;
  Payment? _payment;
  bool _busy = false;
  String? _error;

  static const _typed = {'text', 'phone', 'money', 'number', 'signed', 'choice', 'account', 'bank'};

  @override
  void initState() {
    super.initState();
    for (final b in _boxes.values) {
      b.addListener(() => setState(() {}));
    }
    final account = _ofKind('account');
    if (account != null) {
      _boxes[account.key]!.addListener(_numberChanged);
      _numberChanged();
    }
    if (_fields.any((f) => !_typed.contains(f.kind))) {
      unawaited(
        widget.auth.api.chatActionChoices(id: widget.action.id).then((r) {
          if (!mounted) return;
          setState(
            () => _choices = {
              for (final e in r.fields.entries) e.key: [for (final x in (e.value as List)) '$x'],
            },
          );
        }, onError: (Object _) {}),
      );
    }
  }

  @override
  void dispose() {
    for (final b in _boxes.values) {
      b.dispose();
    }
    super.dispose();
  }

  /// The account number typed, once it is 10 digits.
  String? get _number {
    final f = _ofKind('account');
    final v = f == null ? '' : _boxes[f.key]!.text.replaceAll(RegExp(r'[\s-]'), '');
    return RegExp(r'^\d{10}$').hasMatch(v) ? v : null;
  }

  /// A new number: its banks read again (the likely ones first), what the
  /// banks said of the old one let go, and the bank chosen checked.
  void _numberChanged() {
    final n = _number;
    if (n == _saidFor) return;
    _said.clear();
    _likelyAsked = const [];
    _saidFor = n;
    if (n == null) return;
    unawaited(
      _loadBanks(n).then((_) {
        final bank = _ofKind('bank');
        final code = bank == null ? '' : _codeOf(_banksChosen[bank.key] ?? '');
        if (code.isNotEmpty) return _check([code]);
      }, onError: (Object _) {}),
    );
  }

  Future<void> _loadBanks(String? number) async {
    if (_banks != null && _banksFor == number) return;
    final r = await widget.auth.api.listPayBanks(account: number);
    if (!mounted) return;
    setState(() => (_banks = r.banks, _banksFor = number));
  }

  /// A bank's code in the list, by its name (as chosen, or as the message
  /// said it); empty when the list is by name alone.
  String _codeOf(String name) {
    final n = name.trim().toLowerCase();
    if (n.isEmpty) return '';
    final list = _banks ?? const <ListPayBanksResponseBanksItem>[];
    return list.where((b) => b.name.toLowerCase() == n).firstOrNull?.code ?? '';
  }

  /// Names are checked before preparing by owners alone (staff pay saved
  /// accounts, checked when saved).
  bool get _owner => (widget.auth.me?.roles as List?)?.contains('owner') ?? false;

  /// The bank's name for the account at these banks (the likely ones, up
  /// to three, when none are named): each answer kept; nothing chosen.
  Future<void> _check(List<String>? codes) async {
    final n = _number;
    if (n == null || !_owner) return;
    setState(() => (_checking = true, _error = null));
    try {
      final r = await widget.auth.api.payNameCheck(body: PayNameCheckRequest(accountNumber: n, bankCodes: codes));
      if (!mounted || _number != n) return;
      setState(() {
        for (final b in r.banks) {
          _said[b.bankCode] = b;
        }
        if (codes == null) _likelyAsked = [for (final b in r.banks) b.bankCode];
      });
    } on Object catch (e) {
      if (mounted && codes == null) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  String _value(ChatField f) => f.kind == 'bank' ? (_banksChosen[f.key] ?? '') : _boxes[f.key]!.text.trim();

  ChatField? _ofKind(String kind) => _fields.where((f) => f.kind == kind).firstOrNull;

  int? get _kobo {
    final f = _ofKind('money');
    return f == null ? null : dealKobo(_value(f));
  }

  /// What is missing or wrong, for the button to wait on.
  String? get _problem {
    for (final f in _fields) {
      if (f.optional != true && _value(f).isEmpty) return '';
    }
    final account = _ofKind('account');
    if (account != null && !RegExp(r'^\d{10}$').hasMatch(_value(account))) return 'An account number is 10 digits.';
    final k = _kobo;
    if (_ofKind('money') != null && (k == null || k < 100)) return 'Say how much, like 45,000 or 45k.';
    return null;
  }

  Future<void> _pickBank(ChatField f) async {
    if (_banks == null || _banksFor != _number) {
      setState(() => _error = null);
      try {
        await _loadBanks(_number);
      } on Object catch (e) {
        if (mounted) setState(() => _error = errorWords(e));
        return;
      }
    }
    if (!mounted) return;
    final list = _banks!;
    final likely = _number != null && list.any((b) => b.likely == true);
    final v = await pickOneOf(
      context,
      title: 'Bank',
      value: _banksChosen[f.key],
      note: likely ? 'The banks this number can be at come first.' : null,
      choices: [for (final b in list) (b.name, b.name, likely && b.likely == true ? 'Likely for this number' : '')],
      searchHint: list.length > 8 ? 'Find the bank: guaranty, kuda…' : null,
    );
    if (v != null && mounted) _choose(f, v);
  }

  /// A bank chosen: the name check runs at once.
  void _choose(ChatField f, String name) {
    setState(() => _banksChosen[f.key] = name);
    final code = _codeOf(name);
    if (code.isNotEmpty && _number != null && !_said.containsKey(code)) unawaited(_check([code]));
  }

  /// The payment prepared, from the form: nothing is sent.
  Future<void> _prepare() async {
    setState(() => (_busy = true, _error = null));
    final values = {for (final f in _fields) f.key: _value(f)};
    final payee = _ofKind('payee');
    final account = _ofKind('account');
    final bank = _ofKind('bank');
    final note = _fields.where((f) => f.kind == 'text').firstOrNull;
    final what = _fields.where((f) => f.kind == 'choice').firstOrNull;
    CreatePaymentRequest body({bool mine = false}) => CreatePaymentRequest(
      amountKobo: _kobo!,
      riderId: widget.riderId,
      payee: payee == null ? null : values[payee.key]!.replaceFirst(RegExp('^@'), ''),
      mine: mine ? true : null,
      accountNumber: account == null ? null : values[account.key],
      bankName: bank == null ? null : values[bank.key],
      purpose: what == null ? 'other' : purposeOf(values[what.key]!),
      note: note == null || values[note.key]!.isEmpty ? null : values[note.key],
      from: widget.from,
    );
    try {
      Payment p;
      try {
        p = (await widget.auth.api.createPayment(body: body())).payment;
      } on ApiError catch (e) {
        // An owner's own saved account is paid by its name too.
        if (e.body['code'] != 'no_payee' || payee == null) rethrow;
        try {
          p = (await widget.auth.api.createPayment(body: body(mine: true))).payment;
        } on ApiError {
          throw e;
        }
      }
      if (mounted) setState(() => _payment = p);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _chips(ChatField f, List<String> choices) => TkChips<String>(
    label: f.label,
    padding: EdgeInsets.zero,
    chips: [for (final c in choices) (c, c)],
    on: _boxes[f.key]!.text,
    onPick: (v) => _boxes[f.key]!.text = v,
  );

  Widget _field(ChatField f) {
    final label = f.optional == true ? '${f.label} (optional)' : f.label;
    switch (f.kind) {
      case 'choice':
        return TkKitField(label: f.label, hint: f.hint, field: false, child: _chips(f, f.choices ?? const []));
      case 'bank':
        final pick = TkPickBox(label: f.label, shown: _banksChosen[f.key] ?? 'Choose the bank', onTap: () => unawaited(_pickBank(f)));
        // An account number with no bank yet: the likely banks checked at
        // once, each answer shown, the person picks.
        final likely = _owner && _number != null && _banksFor == _number && (_banks?.any((b) => b.likely == true && b.code.isNotEmpty) ?? false);
        if (!likely || _likelyAsked.isNotEmpty || (_banksChosen[f.key] ?? '').isNotEmpty) {
          return TkKitField(label: f.label, hint: f.hint, field: false, child: pick);
        }
        return TkKitField(
          label: f.label,
          hint: f.hint,
          field: false,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              pick,
              const SizedBox(height: 4),
              Align(
                alignment: Alignment.centerLeft,
                child: TkLink(
                  label: _checking ? 'Checking the likely banks…' : 'Check the likely banks for me',
                  bold: true,
                  onTap: _checking ? null : () => unawaited(_check(null)),
                ),
              ),
            ],
          ),
        );
      default:
        final box = TkKitField(
          label: label,
          hint: f.hint,
          child: TkTextBox(
            controller: _boxes[f.key]!,
            size: 15,
            height: 46,
            max: f.kind == 'text' ? 100 : 150,
            money: f.kind == 'money',
            prefix: f.kind == 'money' ? '₦' : null,
            digits: f.kind == 'account' ? 10 : (f.kind == 'number' ? 6 : null),
            plain: f.kind == 'payee',
          ),
        );
        // The business's saved accounts, as @names: never numbers.
        final choices = _choices[f.key] ?? const <String>[];
        if (choices.isEmpty) return box;
        return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [box, const SizedBox(height: 10), _chips(f, choices)]);
    }
  }

  /// What the likely banks said: one row each, the ones with a name to
  /// pick; the one picked ticked.
  Widget? _answers() {
    final bank = _ofKind('bank');
    if (bank == null || _likelyAsked.isEmpty) return null;
    final look = TkLook.of(context);
    final chosen = (_banksChosen[bank.key] ?? '').toLowerCase();
    return TkSection(
      title: 'What the banks say',
      inset: 56,
      children: [
        for (final code in _likelyAsked)
          if (_said[code] case final b?)
            TkListRow(
              title: b.bankName,
              sub: (b.accountName ?? '').isNotEmpty ? b.accountName! : (b.problem ?? 'The bank did not say just now.'),
              icon: (b.accountName ?? '').isNotEmpty ? 'b-bank' : 'b-x',
              square: (b.accountName ?? '').isNotEmpty ? look.lime : look.red,
              chevron: false,
              trailing: chosen == b.bankName.toLowerCase() ? TkIcon('b-check', size: 20, stroke: 2.4, color: look.limeInk) : null,
              onTap: (b.accountName ?? '').isNotEmpty ? () => _choose(bank, b.bankName) : null,
              semanticsLabel: (b.accountName ?? '').isNotEmpty ? '${b.bankName}: ${b.accountName}. Pay at this bank' : '${b.bankName}: ${b.problem ?? 'no answer'}',
            ),
      ],
    );
  }

  /// What the bank chosen says of the number: its name, or that it has
  /// none (the number kept, another bank to try); and the message's name
  /// when it is not the bank's.
  List<Widget> _bankSays() {
    final bank = _ofKind('bank');
    final n = _number;
    if (bank == null || n == null) return const [];
    final name = _banksChosen[bank.key] ?? '';
    final b = _said[_codeOf(name)];
    if (name.isEmpty || b == null) return const [];
    final look = TkLook.of(context);
    final ok = (b.accountName ?? '').isNotEmpty;
    return [
      if (ok)
        TkCallout(
          leading: TkSquare('b-check', color: look.lime),
          title: 'The bank says ${b.accountName}',
          sub: '${b.bankName} · ••${n.substring(6)}',
          color: look.lime,
        )
      else
        TkCallout(
          leading: TkSquare('b-x', color: look.red),
          title: b.problem ?? 'No account found at ${b.bankName}.',
          sub: 'Try another bank: the number stays.',
          color: look.red,
        ),
      if (ok) ?nameDiffers(context, widget.said, b.accountName!),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final p = _payment;
    if (p != null) {
      return PaymentSheet(key: ValueKey(p.id), auth: widget.auth, payment: p, fresh: true, said: widget.said, onBack: () => setState(() => _payment = null));
    }
    final problem = _problem;
    final look = TkLook.of(context);
    return TkFormSheet(
      title: widget.action.title,
      crumb: 'Checked with the bank before you pay',
      action: _busy ? 'Checking…' : 'Next',
      onAction: _busy || problem != null ? null : _prepare,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(children: [for (final f in _fields) _field(f)]),
        ?_answers(),
        ..._bankSays(),
        if (problem != null && problem.isNotEmpty && _fields.every((f) => f.optional == true || _value(f).isNotEmpty))
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: problem,
            color: look.orange,
          ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-x', color: look.red),
            title: 'Not ready to pay',
            sub: _error,
            subLines: 4,
            color: look.red,
          ),
      ],
    );
  }
}

/// One payment as a sheet: what the bank calls the account, large; who,
/// the bank and the account masked; how much and what for; what to look at
/// first. A payment not sent yet has Pay (the passkey) and Cancel. Closes
/// with the payment once sent.
class PaymentSheet extends StatefulWidget {
  const PaymentSheet({super.key, required this.auth, required this.payment, this.fresh = false, this.onBack, this.said = ''});
  final Auth auth;
  final Payment payment;

  /// The name the message gave, set against the bank's.
  final String said;

  /// Prepared in this sheet just now: let go if the sheet closes unpaid.
  final bool fresh;

  /// Back to the form it was prepared from (the prepared one let go).
  final VoidCallback? onBack;

  @override
  State<PaymentSheet> createState() => _PaymentSheetState();
}

class _PaymentSheetState extends State<PaymentSheet> {
  late Payment _p = widget.payment;
  final _code = TextEditingController();
  bool _busy = false;
  bool _sent = false;
  bool _tried = false;
  // The server said the same payment went in the last day: Pay again.
  late bool _again = widget.payment.again == true;
  String? _againWords;
  // A code from the authenticator, when the phone has no passkey or the
  // server asks for one.
  bool _useCode = false;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _code.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _code.dispose();
    // Prepared here and let go unpaid: nothing is left waiting.
    if (widget.fresh && !_tried && _p.status == 'prepared') {
      unawaited(_auth.api.cancelPayment(id: _p.id).then((_) {}, onError: (Object _) {}));
    }
    super.dispose();
  }

  Future<void> _pay() async {
    setState(() => (_busy = true, _error = null));
    try {
      final code = _useCode ? _code.text.replaceAll(RegExp(r'\D'), '') : '';
      if (code.isEmpty && !await passkeys.available()) {
        setState(() => (_useCode = true, _error = 'This phone cannot use a passkey. Type a code from your authenticator app.'));
        return;
      }
      _tried = true;
      final p = await confirmPaymentWithProof(_auth, _p, again: _again, code: code);
      setState(() => (_p = p, _sent = true));
      // The tick draws, then the sheet goes.
      await Future<void>.delayed(const Duration(milliseconds: 1200));
      if (mounted) Navigator.of(context).pop(p);
    } on ApiError catch (e) {
      final code = e.body['code'];
      setState(() {
        if (code == 'pay_again') {
          // The same payment in the last day: shown, and Pay again.
          _again = true;
          _againWords = e.message;
        } else if (e.status == 403 && e.body['step_up'] != null) {
          final methods = ((e.body['methods'] as List?) ?? const []).whereType<String>().toList();
          if (methods.contains('totp') && (!methods.contains('passkey') || _useCode)) _useCode = true;
          _error = e.message;
        } else {
          _error = e.message;
        }
      });
      if (code == 'not_prepared') unawaited(_reload());
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet: the payment waits.
    } on Object catch (e) {
      setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reload() async {
    try {
      final p = (await _auth.api.getPayment(id: _p.id)).payment;
      if (mounted) setState(() => _p = p);
    } on Object {
      // Shown as it was.
    }
  }

  Future<void> _cancel() async {
    setState(() => (_busy = true, _error = null));
    try {
      final p = (await _auth.api.cancelPayment(id: _p.id)).payment;
      if (!mounted) return;
      setState(() => _p = p);
      showTkToast(context, 'Cancelled: nothing was paid');
      Navigator.of(context).pop();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _p;
    final prepared = p.status == 'prepared';
    final status = paymentStatus(p.status);
    final amount = naira(p.amountKobo);
    final code = _code.text.replaceAll(RegExp(r'\D'), '');
    final fee = p.feeKobo is num ? (p.feeKobo as num).toInt() : null;
    final reason = p.reason is String ? p.reason as String : '';
    final lines = <(String, String)>[
      ('Amount', amount),
      if (fee != null) ('Fee', naira(fee)),
      ('What for', purposeWords(p)),
      if ((p.note ?? '').isNotEmpty) ('Note', p.note!),
      if (!prepared) ('Status', status.words),
      if ((p.needsOk ?? '').isNotEmpty) ('Why it waits', needsOkWords(p.needsOk!)),
      if (reason.isNotEmpty) ('Why', reason),
    ];
    final warnings = <Widget>[
      if (p.accountName is String) ?nameDiffers(context, widget.said, p.accountName as String),
      if (p.changedAccount == true)
        TkCallout(
          leading: TkSquare('b-warn', color: look.red),
          title: 'The account changed since they were last paid',
          sub: 'Check the new number with them by phone first. The owner’s OK is asked before it goes.',
          subLines: 3,
          color: look.red,
        ),
      if (p.firstPayment == true)
        TkCallout(
          leading: TkSquare('b-alert', color: look.orange),
          title: 'First payment to this account',
          sub: 'Check the account number and name with them before you pay.',
          color: look.orange,
        ),
      if (_again && prepared)
        TkCallout(
          leading: TkSquare('b-refund', color: look.orange),
          title: _againWords ?? 'You paid them $amount in the last day',
          sub: 'Pay again only if this is a second payment.',
          color: look.orange,
        ),
    ];
    final top = TkTopCard(
      semanticsLabel: 'The bank says ${paymentName(p)}. ${paymentWhere(p)}. $amount.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(p.accountName is String ? 'The bank says' : 'Paying', color: look.limeInk, icon: 'b-bank'),
          const SizedBox(height: 8),
          Text(paymentName(p), style: tkDisplay(size: 28, lineHeight: 1.15, letterSpacing: -0.8, color: t.text)),
          const SizedBox(height: 4),
          Text(paymentWhere(p), style: tkText(size: 14, lineHeight: 1.35, color: t.muted)),
          const SizedBox(height: 14),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(amount, style: tkFigure(context, size: 34)),
          ),
        ],
      ),
    );
    final children = <Widget>[
      top,
      ...warnings,
      TkFormGroup(children: [TkLines(lines)]),
      if (prepared && _useCode)
        TkFormGroup(
          children: [
            TkKitField(
              label: 'A code from your authenticator app',
              hint: 'No passkey on this phone? Add one in Settings.',
              child: TkTextBox(controller: _code, digits: 6),
            ),
          ],
        ),
      if (prepared && !_useCode)
        Center(
          child: TkLink(label: 'Use a code from your authenticator instead', bold: true, onTap: () => setState(() => _useCode = true)),
        ),
      if (_error != null)
        TkCallout(
          leading: TkSquare('b-x', color: look.red),
          title: 'Not paid',
          sub: _error,
          subLines: 4,
          color: look.red,
        ),
    ];
    final overlay = _sent ? TkSavedTick(words: paymentSentWords(p)) : null;
    if (!prepared) {
      return TkFormSheet(title: 'Payment', crumb: 'Pay', action: 'Done', onAction: () => Navigator.of(context).pop(), overlay: overlay, children: children);
    }
    final ready = !_busy && (!_useCode || code.length == 6);
    return TkFormSheet(
      title: 'Check before you pay',
      crumb: 'Pay',
      overlay: overlay,
      action: _busy ? 'Paying…' : (_again ? 'Pay again $amount' : 'Pay $amount'),
      actionIcon: _useCode ? null : 'fingerprint',
      onAction: ready ? _pay : null,
      second: widget.onBack == null ? 'Cancel it' : 'Back',
      secondDanger: widget.onBack == null,
      onSecond: _busy ? null : (widget.onBack ?? _cancel),
      children: children,
    );
  }
}
