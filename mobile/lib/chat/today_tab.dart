// The home's Today tab (web Home.tsx, lib/home.ts): the owner's view of the
// day. A greeting; money in today (the money log's, else the storefront's
// paid orders) with what waits to be matched; orders today and how many
// wait; the jobs done this week; what needs the person (approvals, approved
// here; forms to fill; payments to match; orders waiting); the latest runs;
// the set-up still to finish; the workflows running for them. Workflows,
// runs and forms are the web's pages: they open in the browser (§3.5).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show Approval, ChatAlert;
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/modal.dart' show openOrSay;
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show showTkSheet;
import '../theme/theme.dart';
import 'alerts.dart' show subjectWords;
import 'approve.dart' show ApproveSheet;
import 'invoice_match.dart' show InvoiceMatchPage, channelWords, unmatchedPath;
import 'money_page.dart' show MoneyPage, lagosDate;
import 'storefront_orders_page.dart' show StorefrontOrderPage, StorefrontOrdersPage;
import 'team_pages.dart' show StaffPage, TasksPage;
import 'words.dart' show CardTone, naira;
import '../kit/mobile.dart' show TkRoute;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

DateTime _lagos(DateTime t) => t.toUtc().add(const Duration(hours: 1));

/// time.ts greeting, by the hour in Lagos.
String greeting(DateTime now) {
  final h = _lagos(now).hour;
  return h < 12 ? 'Good morning' : (h < 17 ? 'Good afternoon' : 'Good evening');
}

const _weekdays = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];
const _months = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const _short = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// "Thursday 12 November".
String todayWords(DateTime now) {
  final l = _lagos(now);
  return '${_weekdays[l.weekday - 1]} ${l.day} ${_months[l.month - 1]}';
}

/// time.ts shortWhen: today's time ("14:05"), else "Yesterday", else the date.
String shortWhen(String iso, DateTime now) {
  final d = DateTime.tryParse(iso);
  if (d == null) return '';
  final l = _lagos(d);
  final today = lagosDate(now);
  final day = '${l.year.toString().padLeft(4, '0')}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
  if (day == today) return '${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
  if (day == lagosDate(now.subtract(const Duration(days: 1)))) return 'Yesterday';
  return '${l.day} ${_short[l.month - 1]}';
}

bool _sameLagosDay(String iso, DateTime now) {
  final d = DateTime.tryParse(iso);
  return d != null && lagosDate(d) == lagosDate(now);
}

/// home.ts moneyToday: orders paid today.
({int kobo, int count}) moneyToday(List<Map<String, dynamic>> orders, DateTime now) {
  var kobo = 0, count = 0;
  for (final o in orders) {
    if (_str(o['paid_at']).isEmpty || o['status'] == 'refunded' || !_sameLagosDay(_str(o['paid_at']), now)) continue;
    kobo += _int(o['total_kobo']);
    count++;
  }
  return (kobo: kobo, count: count);
}

bool _orderWaits(Map<String, dynamic> o) => const {'received', 'price_changed', 'refund_due'}.contains(o['status']);

/// home.ts ordersToday.
({int count, int waiting}) ordersToday(List<Map<String, dynamic>> orders, DateTime now) =>
    (count: orders.where((o) => _sameLagosDay(_str(o['created_at']), now)).length, waiting: orders.where(_orderWaits).length);

/// home.ts runWords.
String runWords(String status) => switch (status) {
  'completed' => 'finished',
  'failed' => 'failed',
  'waiting' => 'is waiting',
  'queued' || 'running' => 'is running',
  'needs_reconciliation' => 'needs checking',
  'cancelled' => 'was stopped',
  _ => status.replaceAll('_', ' '),
};

const _days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/// plaincanvas.ts scheduleOf and scheduleWords: a cron in words, or null.
String? scheduleWords(String cron) {
  final f = cron.trim().split(RegExp(r'\s+'));
  if (f.length != 5 || f[3] != '*') return null;
  int? n(String v, int lo, int hi) {
    final x = RegExp(r'^\d+$').hasMatch(v) ? int.parse(v) : null;
    return x != null && x >= lo && x <= hi ? x : null;
  }

  final minute = n(f[0], 0, 59);
  if (minute == null) return null;
  if (f[1] == '*' && f[2] == '*' && f[4] == '*') return minute == 0 ? 'Every hour, on the hour' : 'Every hour, at $minute minutes past';
  final hour = n(f[1], 0, 23);
  if (hour == null) return null;
  final time = '${hour.toString().padLeft(2, '0')}:${minute.toString().padLeft(2, '0')}';
  if (f[2] == '*' && f[4] == '*') return 'Every day at $time';
  if (f[2] == '*' && f[4] == '1-5') return 'Every weekday (Monday to Friday) at $time';
  if (f[2] == '*') {
    final w = n(f[4], 0, 7);
    return w == null ? null : 'Every ${_days[w % 7]} at $time';
  }
  if (f[4] == '*') {
    final d = n(f[2], 1, 28);
    return d == null ? null : 'On day $d of every month at $time';
  }
  return null;
}

const _linkEvents = {
  'order.received': 'A linked customer sends an order',
  'order.delivered': 'A linked supplier delivers your order',
  'bill.received': 'A linked supplier sends a bill',
  'bill.paid': 'A bill from a linked supplier is paid',
  'thread.message': 'A linked business writes to you',
  'link.accepted': 'A business links with you',
};

String _service(Object? ref) {
  final id = ref is String ? ref.split('@').first : '';
  return id.isEmpty ? 'a service' : id[0].toUpperCase() + id.substring(1);
}

/// home.ts triggerWords: what starts a workflow.
String triggerWords(Map<String, dynamic>? t) {
  if (t == null) return 'Not set up yet';
  final c = _map(t['config']);
  return switch (t['type']) {
    'schedule' => (c['cron'] is String ? scheduleWords(c['cron'] as String) : null) ?? 'On a schedule',
    'manual' => 'When someone starts it',
    'storefront' => 'An order comes in',
    'form' => 'Someone fills in a form',
    'invoice' => c['event'] == 'payment' ? 'A payment is counted towards an invoice' : 'An invoice is paid',
    'link' => _linkEvents[c['event']] ?? 'A linked business does something',
    'whatsapp' => 'A WhatsApp message comes in',
    'ussd' => 'Someone dials your USSD code',
    'email' => 'An email comes in',
    'webhook' => 'Another app tells it',
    'connector_event' => '${_service(c['connector'])} tells it something happened',
    'capability_event' => 'Your payments account tells it about a payment',
    'polling' => 'It checks ${_service(c['connector'])} regularly',
    'database_change' => 'A database changes',
    'subflow' => 'Another workflow starts it',
    _ => 'Something starts it',
  };
}

/// onboarding.ts CHECKLIST: each step's words and where it is done (the web).
const checklist = {
  'verify_email': ('Confirm your email', '/start'),
  'chat': ('Link WhatsApp or Telegram', '/account'),
  'page': ('Make your page', '/storefront'),
  'workflow': ('Pick a template or describe what you want', '/start/guide'),
  'connection': ('Connect what it needs', '/start/guide'),
  'publish': ('Publish it', '/workflows'),
  'first_run': ('Run it once', '/workflows'),
  'invite': ('Invite a teammate', '/members'),
};

class TodayTab extends StatefulWidget {
  const TodayTab({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<TodayTab> createState() => _TodayTabState();
}

class _TodayTabState extends State<TodayTab> with Polling {
  Map<String, dynamic>? _day;
  List<Map<String, dynamic>>? _toMatch;
  List<Map<String, dynamic>>? _orders;
  List<Map<String, dynamic>>? _runs;
  Map<String, dynamic>? _week;
  List<Map<String, dynamic>>? _approvals;
  String? _approvalsError;
  List<Map<String, dynamic>> _forms = const [];
  List<Map<String, dynamic>>? _workflows;
  Map<String, dynamic>? _setup;
  Map<String, dynamic>? _clock;
  List<Map<String, dynamic>>? _tasks;
  String? _runsError;

  Auth get _auth => widget.auth;
  DateTime get _now => (widget.now ?? DateTime.now)();
  bool _area(String key) => (_auth.me?.areas ?? const {})[key] != false;
  bool get _shop => _auth.can('storefront.manage') && _area('orders');
  bool get _money => _auth.can('invoice.read') && _area('money');
  bool get _runsOK => _auth.can('run.read');
  bool get _decides => _auth.can('approval.decide') || _auth.can('workflow.publish');

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<Map<String, dynamic>?> _get(String path, [Map<String, String>? query]) async =>
      (await _auth.transport.send('GET', path, query: query) as Map).cast<String, dynamic>();

  Future<void> _load() async {
    final today = lagosDate(_now);
    await Future.wait([
      if (_money)
        () async {
          try {
            final d = await _get('/v1/money/summary', {'from': today, 'to': today});
            if (mounted) setState(() => _day = d);
          } on Object {
            // The orders' money stands in.
          }
          try {
            final u = await _get(unmatchedPath);
            if (mounted) setState(() => _toMatch = _maps(u?['payments']));
          } on Object {
            if (mounted) setState(() => _toMatch = const []);
          }
        }(),
      if (_shop)
        () async {
          try {
            final o = await _get('/v1/storefront/orders', {'limit': '200'});
            if (mounted) setState(() => _orders = _maps(o?['orders']));
          } on ApiError catch (e) {
            // A 404 is a storefront not turned on: no orders.
            if (mounted && e.status == 404) setState(() => _orders = null);
          } on Object {
            // Shown without them.
          }
        }(),
      if (_runsOK)
        () async {
          try {
            final r = await _get('/v1/runs', {'limit': '6'});
            if (mounted) setState(() => (_runs = _maps(r?['runs']), _runsError = null));
          } on Object catch (e) {
            if (mounted) setState(() => _runsError = errorWords(e));
          }
          try {
            final w = await _get('/v1/dashboard', {'environment': 'prod', 'days': '7', 'tz': 'Africa/Lagos'});
            if (mounted) setState(() => _week = _map(w?['runs']));
          } on Object {
            // No week's figures.
          }
        }(),
      if (_decides)
        () async {
          try {
            final a = await _get('/v1/approvals');
            if (mounted) setState(() => (_approvals = _maps(a?['approvals']), _approvalsError = null));
          } on Object catch (e) {
            if (mounted) setState(() => _approvalsError = errorWords(e));
          }
        }(),
      () async {
        try {
          final f = await _get('/v1/form-requests');
          if (mounted) setState(() => _forms = _maps(f?['requests']));
        } on Object {
          // None to fill.
        }
      }(),
      if (_auth.can('workflow.read'))
        () async {
          try {
            final w = await _get('/v1/workflows', {'tz': 'Africa/Lagos'});
            if (mounted) setState(() => _workflows = _maps(w?['workflows']));
          } on Object {
            // Not shown.
          }
        }(),
      if (_auth.can('member.manage'))
        () async {
          try {
            final c = await _get('/v1/staff/clock');
            if (mounted) setState(() => _clock = c);
          } on Object {
            // No team card's staff line.
          }
        }(),
      () async {
        try {
          final r = await _get('/v1/staff/tasks');
          if (mounted) setState(() => _tasks = _maps(r?['tasks']));
        } on Object {
          // No tasks line.
        }
      }(),
      () async {
        try {
          final o = await _get('/v1/onboarding');
          if (mounted) setState(() => _setup = o);
        } on Object {
          // Not shown.
        }
      }(),
    ]);
  }

  /// One of the web's pages, in the browser (§3.5).
  void _web(String path) {
    final u = Uri.parse('${_auth.transport.base}$path');
    openOrSay(context, () => openInBrowser(u), 'That page could not be opened.');
  }

  Future<void> _go(Widget page) async {
    await Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));
    await _load();
  }

  Future<void> _approve(Map<String, dynamic> a) async {
    final ap = Approval.fromJson(a);
    final card = ChatAlert(at: ap.requestedAt, key: 'approval:${ap.runId}:${ap.stepId}', kind: 'approval', line: ap.workflow, title: ap.stepName, approval: ap);
    final done = await showTkSheet<bool>(context, (_) => ApproveSheet(a: card, decision: 'approved', auth: _auth));
    if (done == true) await _load();
  }

  /// The set-up still to finish (onboarding.ts forAreas, Home's `setup`).
  ({int done, int total, List<String> left})? get _toSetUp {
    final o = _setup;
    if (o == null || o['self_serve'] != true || o['dismissed'] == true) return null;
    final items = _maps(o['items']).where((it) => !(it['id'] == 'page' && !_area('storefront'))).toList();
    final done = items.where((it) => it['done'] == true).length;
    if (o['complete'] == true || done == items.length) return null;
    return (
      done: done,
      total: items.length,
      left: [
        for (final it in items)
          if (it['done'] != true) _str(it['id']),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final now = _now;
    final first = _str(_auth.me?.user?.name).split(' ').first;
    final tenant = _str(_auth.me?.tenantName);
    final orders = _orders;
    final day = _day;
    final counts = orders == null ? null : ordersToday(orders, now);
    final shopMoney = orders == null ? null : moneyToday(orders, now);
    final waitingOrders = orders == null ? const <Map<String, dynamic>>[] : orders.where(_orderWaits).toList();
    final toMatch = _toMatch ?? const <Map<String, dynamic>>[];
    final approvals = _approvals ?? const <Map<String, dynamic>>[];
    final needs = approvals.length + _forms.length + waitingOrders.length + toMatch.length;
    final payments = day == null ? 0 : _maps(day['buckets']).fold(0, (n, b) => n + _int(b['payments']));
    final live = (_workflows ?? const <Map<String, dynamic>>[])
        .where((w) => w['active_version'] != null && w['active_version'] != '' && w['active_version'] != 0)
        .toList();
    final setup = _toSetUp;
    final week = _week;
    return TkPageScreen(
      title: 'Today',
      back: false,
      onRefresh: _load,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('${greeting(now)}${first.isNotEmpty ? ', $first' : ''}', style: tkText(size: 17, weight: 600, color: t.text)),
              Text('${todayWords(now)}${tenant.isNotEmpty ? ' · $tenant' : ''}', style: tkText(size: 14, color: t.muted)),
            ],
          ),
        ),
        if (day != null)
          TkHero(
            label: 'Money in today',
            amount: naira(_int(day['in_settled_kobo'])),
            sub: [
              payments == 1 ? '1 payment' : '$payments payments',
              'Money out ${naira(_int(day['out_settled_kobo']))}',
              if (_toMatch != null) 'To match: ${toMatch.length == 1 ? '1 payment' : '${toMatch.length} payments'}',
            ].join(' · '),
            semanticsLabel: 'Money in today',
            action: TkSmallButton(
              label: 'Money',
              semantics: 'Open Money',
              onPressed: () => _go(MoneyPage(auth: _auth, now: widget.now)),
            ),
          )
        else if (shopMoney != null)
          TkHero(
            label: 'Money in today',
            amount: naira(shopMoney.kobo),
            sub: shopMoney.count == 1 ? '1 payment' : '${shopMoney.count} payments',
            semanticsLabel: 'Money in today',
            action: TkSmallButton(
              label: 'Orders',
              semantics: 'Open orders',
              onPressed: () => _go(StorefrontOrdersPage(auth: _auth, now: widget.now)),
            ),
          ),
        if (counts != null || week != null || (_workflows != null && counts == null))
          TkPageCard(
            title: 'At a glance',
            children: [
              if (counts != null)
                TkRow(
                  name: 'Orders today: ${counts.count}',
                  icon: 'orders',
                  sub: counts.waiting > 0 ? '${counts.waiting} waiting for you' : 'None waiting for you',
                  tone: counts.waiting > 0 ? CardTone.warn : null,
                  onTap: () => _go(StorefrontOrdersPage(auth: _auth, now: widget.now)),
                ),
              if (week != null)
                TkRow(
                  name: 'Jobs done this week: ${_int(week['total'])}',
                  icon: 'runs',
                  sub: _int(week['failed']) > 0
                      ? (_int(week['failed']) == 1 ? '1 needs a look' : '${_int(week['failed'])} need a look')
                      : (_int(week['total']) > 0 ? 'All went well' : 'Nothing yet'),
                  tone: _int(week['failed']) > 0 ? CardTone.warn : null,
                  onTap: () => _web('/runs'),
                ),
              if (_workflows != null && counts == null)
                TkRow(
                  name: 'Working for you: ${live.length}',
                  icon: 'workflows',
                  sub: live.length == 1 ? 'workflow is live' : 'workflows are live',
                  onTap: () => _web('/workflows'),
                ),
            ],
          ),
        TkPageCard(
          title: 'Needs you',
          trailing: needs > 0 ? TkPill('$needs', tone: CardTone.warn) : null,
          children: [
            if (_approvalsError != null) TkErrorBox(_approvalsError!),
            if (needs == 0) Text('Nothing needs you right now.', style: tkHint(t)),
            for (final a in approvals.take(5))
              () {
                final w = subjectWords(a['subject']);
                final step = _str(a['step_name']);
                return Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: Row(
                    children: [
                      Expanded(
                        child: TkRow(
                          name: w.amount != null ? '$step ${w.amount}' : step,
                          icon: 'approvals',
                          tone: a['first_payment'] == true ? CardTone.warn : null,
                          sub:
                              '${a['first_payment'] == true ? 'First payment to this account' : _str(a['workflow'])} · asked ${shortWhen(_str(a['requested_at']), now)}',
                          onTap: () => _web('/approvals'),
                        ),
                      ),
                      const SizedBox(width: 8),
                      TkSmallButton(label: 'Approve', semantics: 'Approve $step', primary: true, onPressed: () => _approve(a)),
                    ],
                  ),
                );
              }(),
            if (approvals.length > 5)
              Align(
                alignment: Alignment.centerLeft,
                child: TkLink(label: 'See all approvals', onTap: () => _web('/approvals')),
              ),
            for (final f in _forms.take(3))
              TkRow(
                name: _str(f['title']),
                icon: 'forms',
                sub: 'To fill in · ${shortWhen(_str(f['created_at']), now)}',
                onTap: () => _web('/forms/requests/${Uri.encodeComponent(_str(f['id']))}'),
              ),
            for (final p in toMatch.take(3))
              TkRow(
                name: 'Match ${p['amount_kobo'] == null ? 'a payment' : naira(p['left_kobo'] is num ? _int(p['left_kobo']) : _int(p['amount_kobo']))}',
                icon: 'money',
                tone: CardTone.warn,
                sub: '${channelWords(_str(p['channel']))} · ${shortWhen(_str(p['received_at']), now)} · no invoice found yet',
                onTap: () => _go(InvoiceMatchPage(auth: _auth)),
              ),
            for (final o in waitingOrders.take(3))
              () {
                final c = _map(o['customer']);
                final who = [_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'a customer');
                return TkRow(
                  name: '${o['status'] == 'refund_due' ? 'Refund' : 'Order'} from $who',
                  icon: o['status'] == 'refund_due' ? 'refund' : 'orders',
                  tone: o['status'] == 'refund_due' ? CardTone.warn : null,
                  sub: '#${o['reference']} · ${naira(_int(o['total_kobo']))} · ${shortWhen(_str(o['created_at']), now)}',
                  onTap: () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Today')),
                );
              }(),
          ],
        ),
        if (_runsOK)
          TkPageCard(
            title: 'Activity',
            trailing: TkLink(label: 'See all', onTap: () => _web('/runs')),
            children: [
              if (_runsError != null) TkErrorBox(_runsError!),
              if (_runs == null && _runsError == null) const TkLoading(rows: 3, label: 'Loading the latest runs…'),
              if (_runs != null && _runs!.isEmpty) Text('Nothing has run yet. Publish a workflow and its runs show here.', style: tkHint(t)),
              for (final r in _runs ?? const <Map<String, dynamic>>[])
                TkRow(
                  name: '${r['workflow']} ${runWords(_str(r['status']))}',
                  icon: 'runs',
                  tone: r['status'] == 'failed' || r['status'] == 'needs_reconciliation' ? CardTone.danger : null,
                  sub: shortWhen(_str(r['started_at']), now),
                  onTap: () => _web('/runs/${Uri.encodeComponent(_str(r['id']))}'),
                ),
            ],
          ),
        if (_clock != null || (_tasks != null && _tasks!.any((x) => x['state'] == 'open')))
          TkPageCard(
            title: 'Your team',
            children: [
              if (_clock case final c?)
                () {
                  final inNow = _maps(c['in']).length, late = _maps(c['late']).length;
                  return TkRow(
                    name: 'Staff',
                    icon: 'members',
                    tone: late > 0 ? CardTone.warn : null,
                    sub: ['$inNow in', if (late > 0) '$late late'].join(' · '),
                    onTap: () => _go(StaffPage(auth: _auth)),
                  );
                }(),
              if (_tasks case final ts?)
                () {
                  final open = ts.where((x) => x['state'] == 'open').toList();
                  final late = open.where((x) => x['late'] == true).length;
                  return TkRow(
                    name: 'Your tasks',
                    icon: 'check',
                    tone: late > 0 ? CardTone.warn : null,
                    sub: open.isEmpty ? 'None open' : '${open.length} open${late > 0 ? ' · $late late' : ''}',
                    onTap: () => _go(TasksPage(auth: _auth, now: widget.now)),
                  );
                }(),
            ],
          ),
        if (setup != null)
          TkPageCard(
            title: 'Finish setting up',
            trailing: Text('${setup.done} of ${setup.total}', style: tkHint(t)),
            children: [
              Semantics(
                label: 'Set-up progress: ${setup.done} of ${setup.total}',
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(4),
                  child: SizedBox(
                    height: 8,
                    child: Stack(
                      children: [
                        Container(color: t.tkSoft),
                        FractionallySizedBox(
                          widthFactor: setup.total == 0 ? 0 : setup.done / setup.total,
                          child: Container(color: t.brand),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 8),
              for (final id in setup.left.take(3))
                if (checklist[id] case final c?) TkRow(name: c.$1, icon: 'check', sub: 'On the web', onTap: () => _web(c.$2)),
              Align(
                alignment: Alignment.centerLeft,
                child: TkLink(label: 'See every step', onTap: () => _web('/start')),
              ),
            ],
          ),
        if (_workflows != null)
          TkPageCard(
            title: 'Running for you',
            trailing: TkLink(label: 'All', onTap: () => _web('/workflows')),
            children: [
              if (live.isEmpty) Text('Nothing yet. Pick a template on the web to get started.', style: tkHint(t)),
              for (final w in live.take(5))
                () {
                  final n = ((w['recent'] as List?) ?? const []).whereType<num>().fold<num>(0, (a, b) => a + b).toInt();
                  return TkRow(
                    name: _str(w['name']),
                    icon: 'workflows',
                    sub:
                        '${triggerWords(w['trigger'] is Map ? _map(w['trigger']) : null)} · ${n == 0 ? 'no runs in 2 weeks' : (n == 1 ? 'ran once in 2 weeks' : 'ran $n times in 2 weeks')}',
                    onTap: () => _web('/workflows/${Uri.encodeComponent(_str(w['id']))}'),
                  );
                }(),
            ],
          ),
        if (!_shop && !_money && !_runsOK && !_decides && _forms.isEmpty)
          const TkEmpty(icon: 'home', title: 'Your day', line: 'What needs you shows here as it comes.'),
      ],
    );
  }
}
