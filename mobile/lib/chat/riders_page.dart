// Riders (web StorefrontRiders.tsx; Dispatch.tsx RidersPayPage, RemitModal,
// BankModal, PaySetupModal; stepup.tsx; lib/riders.ts, lib/dispatch.ts):
// the business's own riders, the areas they cover, where Taskiem reaches
// them and whether they are free; added, changed, on leave, retired; the
// link for riders shared. Pay riders: the cash riders owe (handed in), what
// each is due, their bank accounts (a change confirmed with an
// authenticator code), the weekly payout, paying now, a payout not paid.

import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';

import '../kit/polling.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import '../api/api.g.dart' show Payment;
import 'book_sheet.dart' show lagosTime, whenLabel;
import 'menu_card.dart' show catalogueOf;
import 'pay_sheet.dart' show PaySheet, paymentSentWords, riderPayAction;
import 'saved_fill.dart' show SavedButton, bankCodeIn, pickSavedAccount, pickSavedPhone;
import 'stock_page.dart' show feeInput, feeKobo;
import 'words.dart' show CardTone, naira;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

void _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Riders.
void openRiders(BuildContext context, Auth auth) => _push(context, RidersPage(auth: auth));

/// Opens Pay riders.
void openRidersPay(BuildContext context, Auth auth) => _push(context, RidersPayPage(auth: auth));

/// Riders need text messages: a Taskiem without them answers 409.
bool _off(Object? e) => e is ApiError && e.status == 409;

/// riders.ts stateLine.
String stateLine(Map<String, dynamic> r) {
  final p = (r['presence'] as Map?)?.cast<String, dynamic>() ?? const {};
  if (r['active'] != true) return 'On leave';
  if (p['state'] == 'off') return 'Off';
  if (p['state'] == 'busy') return _str(p['until']).isNotEmpty ? 'Busy till ${lagosTime(_str(p['until']))}' : 'Busy';
  return 'Free';
}

/// riders.ts stateTone.
CardTone stateTone(Map<String, dynamic> r) {
  final s = ((r['presence'] as Map?) ?? const {})['state'];
  if (r['active'] != true || s == 'off') return CardTone.plain;
  return s == 'busy' ? CardTone.warn : CardTone.lime;
}

/// riders.ts riderSub.
String riderSub(Map<String, dynamic> r) {
  final zones = ((r['zones'] as List?) ?? const []).whereType<String>().toList();
  return [
    zones.isNotEmpty ? zones.join(', ') : 'All areas',
    if (_int(r['fee_kobo']) > 0) '${naira(_int(r['fee_kobo']))} a delivery',
    switch (r['reached']) {
      'whatsapp' => 'On WhatsApp',
      'telegram' => 'On Telegram',
      _ => 'By SMS until they tap the link',
    },
    if (r['rating'] is num && _int(r['ratings']) > 0) 'rated ${(r['rating'] as num).toStringAsFixed(1)} of 5 (${_int(r['ratings'])})',
    if (r['not_answering'] == true) 'not answering offers today',
  ].join(' · ');
}

/// A rider on one line: their areas, fee, how Taskiem reaches them and
/// their rating ("Lekki · ₦1,000 a trip · WhatsApp · ★ 4.5").
String riderLine(Map<String, dynamic> r) {
  final zones = ((r['zones'] as List?) ?? const []).whereType<String>().toList();
  return [
    zones.isNotEmpty ? zones.join(', ') : 'All areas',
    if (_int(r['fee_kobo']) > 0) '${naira(_int(r['fee_kobo']))} a trip',
    switch (r['reached']) {
      'whatsapp' => 'WhatsApp',
      'telegram' => 'Telegram',
      _ => 'text',
    },
    if (r['rating'] is num && _int(r['ratings']) > 0) '★ ${(r['rating'] as num).toStringAsFixed(1)}',
    if (r['not_answering'] == true) 'not answering today',
  ].join(' · ');
}

/// riders.ts riderProblem.
String? riderProblem({required String name, required String phone, required String fee, required bool adding}) {
  final n = name.trim();
  if (n.isEmpty) return 'Enter the rider’s name.';
  if (n.length > 60) return 'A name is up to 60 characters.';
  if (adding && phone.replaceAll(RegExp(r'\D'), '').length < 10) return 'Enter the rider’s mobile number, like 0803 123 4567.';
  final k = feeKobo(fee);
  if (k == null || k > 10000000) return 'The fee per delivery is an amount from ₦0 to ₦100,000.';
  return null;
}

/// orders.ts zoneLine.
String zoneLine(Map<String, dynamic> z) => '${z['name']}, ${_int(z['fee_kobo']) > 0 ? '${naira(_int(z['fee_kobo']))} delivery' : 'free delivery'}';

/// dispatch.ts payLineSub.
String payLineSub(Map<String, dynamic> l) {
  final n = _int(l['deliveries']);
  return [
    '$n ${n == 1 ? 'delivery' : 'deliveries'}, fees ${naira(_int(l['fees_kobo']))}',
    if (_int(l['owed_kobo']) > 0) 'less ${naira(_int(l['owed_kobo']))} cash they owe',
    _str(l['bank']).isNotEmpty ? _str(l['bank']) : 'No bank account yet',
  ].join(' · ');
}

/// dispatch.ts payoutSub.
String payoutSub(Map<String, dynamic> x) {
  final n = _int(x['deliveries']);
  var s = '$n ${n == 1 ? 'delivery' : 'deliveries'}';
  if (_int(x['owed_kobo']) > 0) s += ', ${naira(_int(x['owed_kobo']))} cash taken off';
  if (_str(x['created_at']).isNotEmpty) s += ' · started ${whenLabel(_str(x['created_at']))}';
  return s;
}

/// dispatch.ts PAYOUT_STATUS.
({String label, CardTone tone}) payoutStatus(String s) => switch (s) {
  'pending' => (label: 'Being paid', tone: CardTone.warn),
  'paid' => (label: 'Paid', tone: CardTone.lime),
  'failed' => (label: 'Not paid', tone: CardTone.danger),
  'cancelled' => (label: 'Not approved', tone: CardTone.plain),
  _ => (label: s, tone: CardTone.plain),
};

/// dispatch.ts bankProblem.
String? bankProblem(String code, String number) {
  if (!RegExp(r'^[0-9A-Za-z]{3,6}$').hasMatch(code.trim())) return 'Choose the rider’s bank.';
  if (!RegExp(r'^\d{10}$').hasMatch(number.trim())) return 'An account number is 10 digits.';
  return null;
}

/// dispatch.ts remitProblem.
String? remitProblem(String amount, int owed) {
  final k = feeKobo(amount);
  if (k == null || k <= 0) return 'Enter the amount, like 6500.';
  if (k > owed) return 'They owe ${naira(owed)}, less than that.';
  return null;
}

const _weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/// stepup.tsx useStepUp: sent as it is; when the server asks the person to
/// confirm it is them, asked for a code from their authenticator app and
/// sent again with it. A change that takes only a passkey says so: passkeys
/// come to the app with M4.
Future<T> withStepUp<T>(BuildContext context, String title, Future<T> Function(Map<String, Object> proof) send) async {
  try {
    return await send(const {});
  } on ApiError catch (e) {
    if (e.status != 403 || e.body['step_up'] != 'required') rethrow;
    final methods = ((e.body['methods'] as List?) ?? const []).whereType<String>().toList();
    if (!methods.contains('totp')) {
      throw ApiError(403, {
        'error': methods.contains('passkey')
            ? 'This change is confirmed with your passkey, which the app does not use yet. Make it on a device with your passkey.'
            : '${e.message} Go to Account.',
      });
    }
    if (!context.mounted) rethrow;
    final out = await showTkModal<({T value})>(
      context,
      title: 'Confirm: $title',
      builder: (_) => _StepUp<T>(send: send),
    );
    if (out == null) throw ApiError(400, {'error': 'Not confirmed: nothing was changed.'});
    return out.value;
  }
}

class _StepUp<T> extends StatefulWidget {
  const _StepUp({required this.send});
  final Future<T> Function(Map<String, Object> proof) send;

  @override
  State<_StepUp<T>> createState() => _StepUpState<T>();
}

class _StepUpState<T> extends State<_StepUp<T>> {
  final _code = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _code.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final code = _code.text.replaceAll(RegExp(r'\D'), '');
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Confirm it is you. The confirmation is good for this change only.', style: tkText(size: 15, color: t.text)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Code from your authenticator app',
          child: TkTextBox(controller: _code, digits: 6),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Confirm',
              primary: true,
              enabled: !_busy && code.length == 6,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  final v = await widget.send({'totp': code});
                  if (context.mounted) Navigator.of(context).pop((value: v));
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}

// --- riders ---

/// The business's own riders.
class RidersPage extends StatefulWidget {
  const RidersPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<RidersPage> createState() => _RidersPageState();
}

class _RidersPageState extends State<RidersPage> with Polling {
  bool _retired = false;
  Map<String, dynamic>? _view;
  int? _due;
  int? _owedTotal;
  Object? _error;
  bool _busy = false;
  String? _actError;
  // Paying people is on here (money.pay and a payments account): Pay on a
  // rider with a bank account kept.
  bool _pays = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 60), _load);
    if (_auth.can('money.pay')) {
      catalogueOf(_auth).then((topics) {
        final on = topics.any((t) => t.actions.any((a) => a.id == 'pay.person'));
        if (mounted && on) setState(() => _pays = true);
      }, onError: (Object _) {});
    }
  }

  Future<void> _load() async {
    try {
      final v = (await _auth.transport.send('GET', '/v1/riders', query: _retired ? {'retired': 'true'} : null) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_view = v, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
      return;
    }
    // What Pay riders says, on its row: due, and the cash owed.
    try {
      final r = (await _auth.transport.send('GET', '/v1/riders/pay') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _due = _maps(r['lines']).fold<int>(0, (n, l) => n + _int(l['net_kobo'])));
    } on Object {
      // The row says what it is for.
    }
    try {
      final r = (await _auth.transport.send('GET', '/v1/riders/owed') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _owedTotal = _maps(r['owed']).fold<int>(0, (n, o) => n + _int(o['owed_kobo'])));
    } on Object {
      // As above.
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final v = _view;
    final riders = v == null ? const <Map<String, dynamic>>[] : _maps(v['riders']);
    final zones = v == null ? const <Map<String, dynamic>>[] : _maps(v['zones']);
    final url = _str(v?['tap_in_url']);
    String state(Map<String, dynamic> r) => r['active'] != true ? 'leave' : (_str((r['presence'] as Map?)?['state']).ifEmpty('free'));
    final free = riders.where((r) => state(r) == 'free').length;
    final busy = riders.where((r) => state(r) == 'busy').length;
    ({Color fill, Color ink}) accent(Map<String, dynamic> r) => switch (state(r)) {
      'free' => (fill: look.lime, ink: look.limeInk),
      'busy' => (fill: look.blue, ink: look.blueInk),
      _ => (fill: t.muted, ink: t.muted),
    };
    final order = {'free': 0, 'busy': 1, 'off': 2, 'leave': 3};
    final sorted = [...riders]..sort((a, b) => (order[state(a)] ?? 2) - (order[state(b)] ?? 2));
    final due = _due;
    final owed = _owedTotal;
    return TkPageScreen(
      title: 'Riders',
      titleSize: 30,
      onRefresh: _load,
      action: v == null ? null : TkHeadButton(icon: 'b-plus', label: 'Add a rider', primary: true, onTap: () => _edit(null)),
      under: TkChips<bool>(
        label: 'Show',
        chips: const [(false, 'Current'), (true, 'Retired')],
        on: _retired,
        onPick: (x) {
          if (x == _retired) return;
          setState(() => (_retired = x, _view = null));
          _load();
        },
      ),
      children: [
        if (_off(_error))
          const TkEmpty(icon: 'members', title: 'Riders need text messages', line: 'Text messages are not set up on this Taskiem yet.')
        else if (_error != null)
          TkErrorBox(errorWords(_error!)),
        if (_actError != null) TkErrorBox(_actError!),
        if (v == null && _error == null) const TkLoading(rows: 4, label: 'Loading your riders…'),
        if (v != null && !_retired && riders.isNotEmpty)
          TkTopCard(
            semanticsLabel: 'Riders now',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Free now', color: look.limeInk, trailing: '${riders.length} ${riders.length == 1 ? 'rider' : 'riders'}'),
                const SizedBox(height: 6),
                Text.rich(
                  TextSpan(
                    children: [
                      TextSpan(text: '$free', style: tkFigure(context, size: 40)),
                      TextSpan(text: ' free${busy > 0 ? ' · $busy out' : ''}', style: tkDisplay(size: 22, color: t.muted)),
                    ],
                  ),
                ),
                const SizedBox(height: 14),
                TkFaceRow(
                  faces: [
                    for (final r in sorted)
                      (
                        name: _str(r['name']),
                        dot: state(r) == 'leave' || state(r) == 'off' ? t.muted.withValues(alpha: 0.5) : accent(r).fill,
                        under: switch (state(r)) {
                          'free' => 'free',
                          'busy' => _str((r['presence'] as Map?)?['until']).isNotEmpty ? 'till ${lagosTime(_str((r['presence'] as Map)['until']))}' : 'busy',
                          'leave' => 'on leave',
                          _ => 'off',
                        },
                      ),
                  ],
                ),
                if (url.isNotEmpty) ...[
                  const SizedBox(height: 14),
                  TkCallout(
                    leading: TkSquare('b-share', color: look.teal),
                    title: 'The link for riders',
                    sub: 'One tap gets them your deliveries on WhatsApp',
                    color: look.teal,
                    button: 'Share',
                    semantics: 'Share the link for riders',
                    onPressed: () => _share(url),
                  ),
                ],
              ],
            ),
          ),
        if (v != null && !_retired)
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Pay riders',
                sub: owed != null && owed > 0 ? '${naira(owed)} cash owed to you' : (due != null ? 'Due to riders' : 'What they are due, and the cash they owe'),
                icon: 'b-cash',
                square: look.lime,
                amount: due == null ? null : naira(due),
                onTap: () => openRidersPay(context, _auth),
                semanticsLabel: 'Pay riders',
              ),
            ],
          ),
        if (v != null)
          TkSection(
            title: _retired ? 'Retired riders' : 'Your riders',
            empty: _retired ? 'No retired riders.' : 'No riders yet. Add the people who deliver your orders.',
            children: [
              for (final r in sorted)
                TkListRow(
                  title: _str(r['name']),
                  sub: riderLine(r),
                  status: _retired ? null : stateLine(r),
                  statusAccent: _retired ? null : accent(r),
                  onTap: _retired ? null : () => _more(r),
                  trailing: !_retired && _pays && _str(r['bank']).isNotEmpty
                      ? TkSmallButton(label: 'Pay', semantics: 'Pay ${_str(r['name'])}', onPressed: () => _pay(r))
                      : null,
                ),
            ],
          ),
        if (v != null && !_retired && zones.isNotEmpty)
          TkSection(
            title: 'Areas and fees',
            inset: 56,
            children: [
              for (final z in zones)
                TkListRow(
                  title: _str(z['name']),
                  icon: 'b-pin',
                  square: look.teal,
                  amount: _int(z['fee_kobo']) > 0 ? naira(_int(z['fee_kobo'])) : 'No fee',
                  semanticsLabel: zoneLine(z),
                ),
            ],
          ),
      ],
    );
  }

  /// Share pressed: copy the link, or send it on WhatsApp.
  Future<void> _share(String url) async {
    final pick = await showTkModalChoices(context, title: 'The link for riders', choices: [('copy', 'Copy the link'), ('wa', 'Share on WhatsApp')]);
    if (!mounted) return;
    if (pick == 'copy') {
      await Clipboard.setData(ClipboardData(text: url));
      if (mounted) showTkToast(context, 'Copied');
      return;
    }
    if (pick == 'wa') {
      await openOrSay(
        context,
        () => openOutside(Uri.parse('https://wa.me/?text=${Uri.encodeComponent('Tap to get our deliveries on WhatsApp: $url')}')),
        'WhatsApp could not be opened.',
      );
    }
  }

  /// Pay on a rider: the Pay sheet, to the bank account kept for their
  /// payouts, for a delivery unless changed.
  Future<void> _pay(Map<String, dynamic> r) async {
    final name = _str(r['name']);
    final paid = await showTkSheet<Payment>(
      context,
      (_) => PaySheet(auth: _auth, action: riderPayAction(name), riderId: _str(r['id']), initial: const {'for': 'a delivery'}, from: 'tools'),
    );
    if (paid != null && mounted) showTkToast(context, '${paymentSentWords(paid)}: ${naira(paid.amountKobo)} to $name');
  }

  /// A rider pressed: change them, send the link again, retire them.
  Future<void> _more(Map<String, dynamic> r) async {
    if (_busy) return;
    final name = _str(r['name']);
    final pick = await showTkModalChoices(
      context,
      title: name,
      choices: [('change', 'Change'), if (r['reached'] == 'sms') ('link', 'Send the link again'), ('retire', 'Retire')],
    );
    if (!mounted) return;
    switch (pick) {
      case 'change':
        await _edit(r);
      case 'link':
        await _run(() => _auth.transport.send('POST', '/v1/riders/${Uri.encodeComponent(_str(r['id']))}/invite', body: const {}), 'Link sent to $name');
      case 'retire':
        final yes = await showTkConfirm(
          context,
          title: 'Retire $name?',
          body: 'They are offered no more deliveries, and keep the ones they made.',
          ok: 'Retire',
          cancel: 'Keep them',
          danger: true,
        );
        if (!yes) return;
        await _run(() => _auth.transport.send('POST', '/v1/riders/${Uri.encodeComponent(_str(r['id']))}/retire', body: const {}), '$name retired');
    }
  }

  Future<void> _edit(Map<String, dynamic>? r) async {
    final zones = _maps(_view?['zones']);
    final said = await showTkModal<String>(
      context,
      title: r == null ? 'Add a rider' : 'Change ${r['name']}',
      builder: (_) => _RiderForm(auth: _auth, rider: r, zones: zones),
    );
    if (said == null || !mounted) return;
    showTkToast(context, said);
    if (_retired) setState(() => _retired = false);
    await _load();
  }
}

/// Add a rider, or change one: their name, areas, fee, and on leave.
class _RiderForm extends StatefulWidget {
  const _RiderForm({required this.auth, required this.rider, required this.zones});
  final Auth auth;
  final Map<String, dynamic>? rider;
  final List<Map<String, dynamic>> zones;

  @override
  State<_RiderForm> createState() => _RiderFormState();
}

class _RiderFormState extends State<_RiderForm> {
  late final Map<String, dynamic>? _r = widget.rider;
  bool get _adding => _r == null;
  late final _name = TextEditingController(text: _str(_r?['name']));
  final _phone = TextEditingController();
  late final _fee = TextEditingController(text: _int(_r?['fee_kobo']) > 0 ? feeInput(_int(_r?['fee_kobo'])) : '');
  late final _labels = TextEditingController(text: widget.zones.isEmpty ? ((_r?['zones'] as List?) ?? const []).join(', ') : '');
  late final Set<String> _zones = {...((_r?['zones'] as List?) ?? const []).whereType<String>()};
  late bool _leave = _r != null && _r['active'] != true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _phone, _fee, _labels]) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in [_name, _phone, _fee, _labels]) {
      c.dispose();
    }
    super.dispose();
  }

  /// A saved person's or place's phone (saved objects §6.4): the phone
  /// only, one picker at a time.
  bool _picking = false;
  Future<void> _phoneFromSaved() async {
    if (_picking) return;
    setState(() => _picking = true);
    try {
      final v = await pickSavedPhone(context, widget.auth);
      if (v != null && mounted) _phone.text = v;
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  List<String> get _chosen => widget.zones.isNotEmpty ? _zones.toList() : _labels.text.split(',').map((z) => z.trim()).where((z) => z.isNotEmpty).toList();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = riderProblem(name: _name.text, phone: _phone.text, fee: _fee.text, adding: _adding);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkKitField(
          label: 'Name',
          child: TkTextBox(controller: _name, max: 60),
        ),
        const SizedBox(height: 10),
        if (_adding)
          TkKitField(
            label: 'Mobile number',
            hint: 'They get the link to your deliveries by SMS on this number.',
            child: Row(
              children: [
                Expanded(child: TkTextBox(controller: _phone, phone: true, hint: '0803 123 4567')),
                const SizedBox(width: 8),
                SavedButton(semantics: 'Pick a saved phone number', picking: _picking, busy: _busy, onPressed: _phoneFromSaved),
              ],
            ),
          )
        else
          Text('Mobile number: ${_r!['phone']}. To use another number, retire ${_r['name']} and add the new one.', style: tkHint(t)),
        const SizedBox(height: 10),
        if (widget.zones.isNotEmpty) ...[
          Text('Areas they cover', style: tkText(size: 13, weight: 600, color: t.muted)),
          for (final z in widget.zones)
            TkChoiceRow(
              control: TkCheckbox(checked: _zones.contains(z['name'])),
              label: Text(zoneLine(z), style: tkText(size: 15, color: t.text)),
              onTap: () => setState(() => _zones.contains(z['name']) ? _zones.remove(z['name']) : _zones.add(_str(z['name']))),
            ),
        ] else
          TkKitField(
            label: 'Areas they cover',
            hint: 'Separated by commas. The areas you deliver to, with their fees, are on your order form.',
            child: TkTextBox(controller: _labels, hint: 'Lekki, Ajah'),
          ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Fee per delivery (₦)',
          hint: 'What you pay them for each delivery. Leave it empty if you pay them another way.',
          child: TkTextBox(controller: _fee, money: true, hint: '1000'),
        ),
        if (!_adding)
          TkChoiceRow(
            control: TkCheckbox(checked: _leave),
            label: Text('On leave: keep them, but offer them nothing', style: tkText(size: 15, color: t.text)),
            onTap: () => setState(() => _leave = !_leave),
          ),
        if (problem != null && _name.text.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: _adding ? 'Add the rider' : 'Save',
              primary: true,
              enabled: !_busy && problem == null,
              onPressed: () async {
                final name = _name.text.trim().replaceAll(RegExp(r'\s+'), ' ');
                final body = {'name': name, 'zones': _chosen, 'fee_kobo': feeKobo(_fee.text) ?? 0};
                setState(() => (_busy = true, _error = null));
                try {
                  if (_adding) {
                    await widget.auth.transport.send('POST', '/v1/riders', body: {...body, 'phone': _phone.text.trim()});
                  } else {
                    await widget.auth.transport.send('PATCH', '/v1/riders/${Uri.encodeComponent(_str(_r!['id']))}', body: {...body, 'active': !_leave});
                  }
                  if (context.mounted) Navigator.of(context).pop(_adding ? '$name added; the link is on its way by SMS' : 'Saved');
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}

// --- pay ---

/// Riders > Pay: the cash riders owe, what each is due, their bank
/// accounts, the weekly payout and paying now.
class RidersPayPage extends StatefulWidget {
  const RidersPayPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<RidersPayPage> createState() => _RidersPayPageState();
}

class _RidersPayPageState extends State<RidersPayPage> {
  List<Map<String, dynamic>>? _owed;
  Map<String, dynamic>? _pay;
  Object? _error;
  bool _busy = false;
  String? _actError;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    // Each on its own: one that fails leaves the other shown.
    Object? failed;
    await Future.wait([
      () async {
        try {
          final r = (await _auth.transport.send('GET', '/v1/riders/owed') as Map).cast<String, dynamic>();
          if (mounted) setState(() => _owed = _maps(r['owed']));
        } on Object catch (e) {
          failed ??= e;
        }
      }(),
      () async {
        try {
          final r = (await _auth.transport.send('GET', '/v1/riders/pay') as Map).cast<String, dynamic>();
          if (mounted) setState(() => _pay = r);
        } on Object catch (e) {
          failed ??= e;
        }
      }(),
    ]);
    if (mounted) setState(() => _error = failed);
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _pay;
    final owed = _owed;
    final lines = p == null ? const <Map<String, dynamic>>[] : _maps(p['lines']);
    final payouts = p == null ? const <Map<String, dynamic>>[] : _maps(p['payouts']);
    final due = lines.fold(0, (n, l) => n + _int(l['net_kobo']));
    final deliveries = lines.fold(0, (n, l) => n + _int(l['deliveries']));
    final ready = _str(p?['workflow_id']).isNotEmpty;
    ({Color fill, Color ink}) tone(CardTone c) => switch (c) {
      CardTone.lime => (fill: look.lime, ink: look.limeInk),
      CardTone.warn => (fill: look.blue, ink: look.blueInk),
      CardTone.danger => (fill: look.red, ink: look.redInk),
      _ => (fill: t.muted, ink: t.muted),
    };
    return TkPageScreen(
      title: 'Pay riders',
      titleSize: 22,
      backLabel: 'Back to Riders',
      onRefresh: _load,
      children: [
        if (_off(_error))
          const TkEmpty(icon: 'members', title: 'Riders need text messages', line: 'Text messages are not set up on this Taskiem yet.')
        else if (_error != null)
          TkErrorBox(errorWords(_error!)),
        if (_actError != null) TkErrorBox(_actError!),
        if (owed == null && p == null && _error == null) const TkLoading(rows: 4, label: 'Loading riders’ pay…'),
        if (p != null || (owed != null && owed.isNotEmpty))
          TkTopCard(
            semanticsLabel: 'Riders’ pay',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (p != null) ...[
                  TkEyebrow('Due to riders now', color: look.limeInk, trailing: '${lines.length} ${lines.length == 1 ? 'rider' : 'riders'}'),
                  const SizedBox(height: 6),
                  FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(due, style: tkFigure(context, size: 40))),
                  Text(
                    [
                      '$deliveries ${deliveries == 1 ? 'delivery' : 'deliveries'}',
                      'their fees, less cash they owe',
                      if (!ready) 'weekly pay not set up',
                    ].join(' · '),
                    style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
                  ),
                  const SizedBox(height: 14),
                  TkButtonRow([
                    TkSmallButton(
                      label: 'Pay riders now',
                      primary: true,
                      onPressed: _busy || !ready || due == 0
                          ? null
                          : () async {
                              final yes = await showTkConfirm(
                                context,
                                title: 'Pay your riders ${naira(due)} now?',
                                body: 'You approve it first, in Taskiem’s conversation.',
                                ok: 'Pay them',
                                cancel: 'Not now',
                              );
                              if (!yes) return;
                              await _run(() => _auth.transport.send('POST', '/v1/riders/pay', body: const {}), 'Started: approve it in Taskiem’s conversation');
                            },
                    ),
                    TkSmallButton(label: 'Weekly pay', semantics: ready ? 'Change the weekly pay' : 'Set up weekly pay', onPressed: _setup),
                  ]),
                ],
                // Cash customers paid at the door, owed by the rider.
                for (final o in owed ?? const <Map<String, dynamic>>[]) ...[
                  const SizedBox(height: 12),
                  TkCallout(
                    leading: TkSquare('b-cash', color: look.orange),
                    title: '${o['rider']} owes ${naira(_int(o['owed_kobo']))} cash',
                    sub:
                        'From ${_int(o['deliveries'])} ${_int(o['deliveries']) == 1 ? 'delivery' : 'deliveries'} paid at the door${_str(o['since']).isNotEmpty ? ' since ${whenLabel(_str(o['since']))}' : ''}',
                    color: look.orange,
                    button: 'Handed in',
                    semantics: 'Record cash handed in by ${o['rider']}',
                    onPressed: () => _remit(o),
                  ),
                ],
              ],
            ),
          ),
        if (owed != null && owed.isEmpty && p != null) Text('No rider owes you cash. A transfer from a rider naming the order counts by itself.', style: tkHint(t)),
        if (p != null)
          TkSection(
            title: 'Due to each rider',
            empty: 'Nothing is due: riders’ fees show here once they deliver.',
            children: [
              for (final l in lines)
                () {
                  final bank = _str(l['bank']).isNotEmpty;
                  final n = _int(l['deliveries']);
                  return TkListRow(
                    title: _str(l['rider']),
                    sub: [
                      '$n ${n == 1 ? 'delivery' : 'deliveries'}',
                      if (_int(l['owed_kobo']) > 0) 'less ${naira(_int(l['owed_kobo']))} cash',
                      bank ? _str(l['bank']) : 'no bank account',
                    ].join(' · '),
                    amount: naira(_int(l['net_kobo'])),
                    status: bank ? null : 'Add bank',
                    statusAccent: (fill: look.orange, ink: look.orangeInk),
                    onTap: () => _bank(l),
                    semanticsLabel: '${bank ? 'Change bank account' : 'Add bank account'} for ${l['rider']}',
                  );
                }(),
            ],
          ),
        if (payouts.isNotEmpty)
          TkSection(
            title: 'Recent payouts',
            children: [
              for (final x in payouts)
                TkListRow(
                  title: _str(x['rider']),
                  sub: payoutSub(x),
                  amount: naira(_int(x['amount_kobo'])),
                  status: payoutStatus(_str(x['status'])).label,
                  statusAccent: tone(payoutStatus(_str(x['status'])).tone),
                  semanticsLabel: x['status'] == 'pending' ? 'Mark not paid for ${x['rider']}' : null,
                  onTap: x['status'] != 'pending' || _busy
                      ? null
                      : () async {
                          final yes = await showTkConfirm(
                            context,
                            title: 'Mark ${x['rider']}’s payout of ${naira(_int(x['amount_kobo']))} as not paid?',
                            body: 'Only once your bank shows it did not go: their deliveries are then paid in the next payout, so marking a payment that went through pays them twice.',
                            ok: 'Mark not paid',
                            cancel: 'Keep it',
                            danger: true,
                          );
                          if (!yes) return;
                          await _run(
                            () => _auth.transport.send('POST', '/v1/riders/payouts/${Uri.encodeComponent(_str(x['payout_id']))}/not-paid', body: const {}),
                            'Marked not paid: paid in the next payout',
                          );
                        },
                ),
            ],
          ),
      ],
    );
  }

  Future<void> _remit(Map<String, dynamic> o) async {
    final done = await showTkModal<bool>(
      context,
      title: 'Cash from ${o['rider']}',
      builder: (_) => _Remit(auth: _auth, owed: o),
    );
    if (done == true && mounted) {
      showTkToast(context, 'Recorded');
      await _load();
    }
  }

  Future<void> _bank(Map<String, dynamic> l) async {
    final done = await showTkModal<bool>(
      context,
      title: '${l['rider']}’s bank account',
      builder: (_) => _Bank(auth: _auth, line: l),
    );
    if (done == true && mounted) {
      showTkToast(context, 'Saved');
      await _load();
    }
  }

  Future<void> _setup() async {
    final done = await showTkModal<bool>(
      context,
      title: 'Weekly pay',
      builder: (_) => _PaySetup(auth: _auth),
    );
    if (done == true && mounted) {
      showTkToast(context, 'Saved: riders are paid weekly after you approve');
      await _load();
    }
  }
}

class _Remit extends StatefulWidget {
  const _Remit({required this.auth, required this.owed});
  final Auth auth;
  final Map<String, dynamic> owed;

  @override
  State<_Remit> createState() => _RemitState();
}

class _RemitState extends State<_Remit> {
  late final _amount = TextEditingController(text: feeInput(_int(widget.owed['owed_kobo'])));
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _amount.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final owed = _int(widget.owed['owed_kobo']);
    final problem = remitProblem(_amount.text, owed);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('They owe ${naira(owed)}. The cash is added to your money log.', style: tkHint(t)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Amount handed in (₦)',
          child: TkTextBox(controller: _amount, money: true),
        ),
        if (problem != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Record it',
              primary: true,
              enabled: !_busy && problem == null,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  await widget.auth.transport.send(
                    'POST',
                    '/v1/riders/${Uri.encodeComponent(_str(widget.owed['rider_id']))}/remit',
                    body: {'amount_kobo': feeKobo(_amount.text), 'how': 'cash'},
                  );
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

class _Bank extends StatefulWidget {
  const _Bank({required this.auth, required this.line});
  final Auth auth;
  final Map<String, dynamic> line;

  @override
  State<_Bank> createState() => _BankState();
}

class _BankState extends State<_Bank> {
  List<({String code, String name})>? _banks;
  String _code = '';
  final _codeBox = TextEditingController();
  final _number = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _codeBox.addListener(() => setState(() {}));
    _number.addListener(() => setState(() {}));
    widget.auth.transport
        .send('GET', '/v1/receive-money/banks')
        .then(
          (r) {
            final list = [
              for (final b in _maps((r as Map)['banks']))
                (
                  code: _str(b['code']).isNotEmpty ? _str(b['code']) : _str(b['bank_code']),
                  name: [_str(b['name']), _str(b['bank_name']), _str(b['code']), _str(b['bank_code'])].firstWhere((x) => x.isNotEmpty, orElse: () => ''),
                ),
            ].where((b) => b.code.isNotEmpty).toList();
            if (mounted) setState(() => _banks = list);
          },
          onError: (Object _) {
            if (mounted) setState(() => _banks = const []);
          },
        );
  }

  @override
  void dispose() {
    _codeBox.dispose();
    _number.dispose();
    super.dispose();
  }

  /// A saved bank account (saved objects §6.4): its bank, and its number
  /// once a passkey shows it; one picker at a time.
  bool _picking = false;
  Future<void> _fromSaved() async {
    if (_picking) return;
    setState(() => _picking = true);
    try {
      final a = await pickSavedAccount(context, widget.auth);
      if (a == null || !mounted) return;
      setState(() {
        final banks = _banks ?? const [];
        if (banks.isNotEmpty) {
          final c = bankCodeIn(banks, a);
          if (c.isNotEmpty) _code = c;
        } else if (a.bankCode.isNotEmpty) {
          _codeBox.text = a.bankCode;
        }
        _number.text = a.number;
      });
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  Widget _savedButton() => SavedButton(label: 'A saved account', semantics: 'Fill from a saved bank account', picking: _picking, busy: _busy, onPressed: _fromSaved);

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final banks = _banks;
    final code = banks != null && banks.isNotEmpty ? _code : _codeBox.text.trim();
    final number = _number.text.replaceAll(RegExp(r'\D'), '');
    final problem = bankProblem(code, number);
    final rider = _str(widget.line['rider']);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          'Their payouts go here. You confirm it is you with your authenticator code. Their bank confirms the name before the first payout, and you see it when you approve.',
          style: tkHint(t),
        ),
        const SizedBox(height: 10),
        if (banks == null)
          const TkLoading(rows: 1, label: 'Loading the banks…')
        else if (banks.isNotEmpty)
          TkKitField(
            field: false,
            label: 'Bank',
            child: Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                TkSmallButton(
                  label: banks.where((b) => b.code == _code).map((b) => b.name).firstOrNull ?? 'Choose their bank',
                  semantics: 'Bank: ${banks.where((b) => b.code == _code).map((b) => b.name).firstOrNull ?? 'none chosen'}',
                  onPressed: () async {
                    final v = await showTkModalChoices(context, title: 'Bank', value: _code, choices: [for (final b in banks) (b.code, b.name)]);
                    if (v != null) setState(() => _code = v);
                  },
                ),
                _savedButton(),
              ],
            ),
          )
        else
          TkKitField(
            label: 'Bank code',
            hint: 'The bank’s code, such as 058 for GTBank.',
            child: Row(
              children: [
                Expanded(child: TkTextBox(controller: _codeBox, plain: true, max: 6)),
                const SizedBox(width: 8),
                _savedButton(),
              ],
            ),
          ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Account number',
          child: TkTextBox(controller: _number, digits: 10),
        ),
        if (number.length == 10 && problem != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Save',
              primary: true,
              enabled: !_busy && problem == null,
              onPressed: () async {
                final body = {
                  'bank_code': code,
                  'bank_name': banks?.where((b) => b.code == code).map((b) => b.name).firstOrNull ?? '',
                  'account_number': number,
                };
                setState(() => (_busy = true, _error = null));
                try {
                  await withStepUp(
                    context,
                    '$rider’s bank account',
                    (proof) =>
                        widget.auth.transport.send('PUT', '/v1/riders/${Uri.encodeComponent(_str(widget.line['rider_id']))}/bank', body: {...body, ...proof}),
                  );
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

class _PaySetup extends StatefulWidget {
  const _PaySetup({required this.auth});
  final Auth auth;

  @override
  State<_PaySetup> createState() => _PaySetupState();
}

class _PaySetupState extends State<_PaySetup> {
  int _day = 5;
  final _time = TextEditingController(text: '17:00');
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _time.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _time.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final ok = RegExp(r'^([01]\d|2[0-3]):[0-5]\d$').hasMatch(_time.text.trim());
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          'Each week, Taskiem adds up each rider’s fees, takes off the cash they owe, and checks their bank account names. You approve the list, then each rider is paid once from your Taskiem wallet.',
          style: tkHint(t),
        ),
        const SizedBox(height: 10),
        TkKitField(
          field: false,
          label: 'Day',
          child: Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: _weekdays[_day],
              semantics: 'Day: ${_weekdays[_day]}',
              onPressed: () async {
                final v = await showTkModalChoices(context, title: 'Day', value: '$_day', choices: [for (var i = 0; i < 7; i++) ('$i', _weekdays[i])]);
                if (v != null) setState(() => _day = int.parse(v));
              },
            ),
          ),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Time (Lagos)',
          hint: 'As 17:00.',
          child: TkTextBox(controller: _time, hint: '17:00'),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Save',
              primary: true,
              enabled: !_busy && ok,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  await widget.auth.transport.send('PUT', '/v1/riders/pay/setup', body: {'pay_day': _day, 'pay_time': _time.text.trim()});
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

extension on String {
  String ifEmpty(String other) => isEmpty ? other : this;
}
