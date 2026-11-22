// The home's Today tab (web Home.tsx, lib/home.ts): the owner's view of the
// day. A greeting; money in today (the money log's, else the storefront's
// paid orders) with what waits to be matched; orders today and how many
// wait; the jobs done this week; what needs the person (approvals, approved
// here; forms to fill; payments to match; orders waiting); the latest runs;
// the set-up still to finish; the workflows running for them. Workflows,
// runs and forms are the web's pages: they open in the browser (§3.5).

import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show Approval, ChatAlert, PageSummary;
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/modal.dart' show openOrSay;
import '../kit/polling.dart';
import '../kit/sheet.dart' show showTkSheet;
import '../theme/theme.dart';
import 'alerts.dart' show subjectWords;
import 'approve.dart' show ApproveSheet;
import 'invoice_match.dart' show InvoiceMatchPage, channelWords, unmatchedPath;
import 'bookings_page.dart' show addDays;
import 'money_page.dart' show MoneyPage, hourWords, lagosDate;
import 'storefront_orders_page.dart' show StorefrontOrderPage, StorefrontOrdersPage;
import 'pages_page.dart' show pinnedPageRows;
import 'team_pages.dart' show StaffPage, TasksPage;
import 'words.dart' show naira;
import 'form_request.dart' show FormRequestScreen, sendKeptFormAnswers;
import '../kit/mobile.dart' show TkCountUp, TkListRow, TkRoute, TkScreen, TkSection, tkInitials;
import '../theme/look.dart';

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

/// Money in by the hour as running totals: the total at the start of each
/// hour of a Lagos day, midnight to midnight (25 points), from a day's
/// hourly buckets (/v1/money/summary for one day).
List<double> hourTotals(List<Map<String, dynamic>> buckets) {
  final per = List<double>.filled(24, 0);
  for (final b in buckets) {
    final d = DateTime.tryParse(_str(b['from']));
    if (d == null) continue;
    per[_lagos(d).hour] += _int(b['in_settled_kobo']);
  }
  final out = List<double>.filled(25, 0);
  for (var h = 0; h < 24; h++) {
    out[h + 1] = out[h] + per[h];
  }
  return out;
}

/// The same from orders paid today (a shop without Money): each order in
/// the hour it was paid.
List<double> paidHourTotals(List<Map<String, dynamic>> orders, DateTime now) => hourTotals([
  for (final o in orders)
    if (_str(o['paid_at']).isNotEmpty && o['status'] != 'refunded' && _sameLagosDay(_str(o['paid_at']), now))
      {'from': o['paid_at'], 'in_settled_kobo': o['total_kobo']},
]);

/// A usual day: the days' running totals averaged, hour by hour; null when
/// none of them took any money (no history yet).
List<double>? usualDay(List<List<double>> days) {
  final some = days.where((d) => d.length == 25 && d.last > 0).toList();
  if (some.isEmpty) return null;
  return [for (var h = 0; h < 25; h++) some.fold<double>(0, (n, d) => n + d[h]) / some.length];
}

/// The hour of a moment in Lagos, its minutes as a fraction (13:30 is 13.5).
double lagosHour(DateTime now) {
  final l = _lagos(now);
  return l.hour + l.minute / 60;
}

/// Today against a usual day at the same time: how far ahead (positive)
/// or behind, in kobo, and up, down or about the same (within ₦100 or 3%);
/// null with no usual day to compare with.
({int kobo, bool? up})? versusUsual(int todayKobo, List<double>? usual, double hour) {
  if (usual == null) return null;
  final i = hour.floor().clamp(0, 23);
  final at = usual[i] + (usual[i + 1] - usual[i]) * (hour - i);
  if (at <= 0 && todayKobo <= 0) return null;
  // In whole naira, as the figure itself is said.
  final diff = ((todayKobo - at) / 100).round() * 100;
  final same = diff.abs() < math.max(10000, at * 0.03);
  return (kobo: diff, up: same ? null : diff > 0);
}

/// "₦16,200 ahead of a usual Thursday".
String usualWords(({int kobo, bool? up}) v, DateTime now) {
  final day = _weekdays[_lagos(now).weekday - 1];
  return switch (v.up) {
    true => '${naira(v.kobo)} ahead of a usual $day',
    false => '${naira(-v.kobo)} behind a usual $day',
    null => 'About the same as a usual $day',
  };
}

class TodayTab extends StatefulWidget {
  const TodayTab({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<TodayTab> createState() => _TodayTabState();
}

class _TodayTabState extends State<TodayTab> with Polling {
  Map<String, dynamic>? _day;

  /// The same weekday's money over the last four weeks, averaged hour by
  /// hour, and the day it was worked out for (read once a day, not each
  /// time the tab reads again).
  List<double>? _usual;
  String? _usualFor;
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
  // The business's own pages (pages_page.dart): pinned ones, and All pages.
  List<PageSummary> _pages = const [];
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
          if (_usualFor != today) await _loadUsual(today);
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
          await sendKeptFormAnswers(_auth);
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
      if (_auth.me?.personal != true)
        () async {
          try {
            final r = await _auth.api.listPages();
            if (mounted) setState(() => _pages = r.pages);
          } on Object {
            // No Your pages.
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

  /// A usual day like today: the same weekday, each of the last four weeks.
  Future<void> _loadUsual(String today) async {
    final days = await Future.wait([
      for (var w = 1; w <= 4; w++)
        () async {
          final d = addDays(today, -7 * w);
          try {
            final s = await _get('/v1/money/summary', {'from': d, 'to': d});
            return hourTotals(_maps(s?['buckets']));
          } on Object {
            return const <double>[];
          }
        }(),
    ]);
    if (mounted) setState(() => (_usual = usualDay(days), _usualFor = today));
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
    final look = TkLook.of(context);
    final now = _now;
    final name = _str(_auth.me?.user?.name);
    final first = name.split(' ').first;
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
    final hour = lagosHour(now);
    final weekday = _weekdays[_lagos(now).weekday - 1];
    final clock = '${_lagos(now).hour.toString().padLeft(2, '0')}:${_lagos(now).minute.toString().padLeft(2, '0')}';

    // The top card: money in today, its line through the day and a usual
    // day's under it (Money's figures, else the shop's paid orders).
    Widget? top;
    if (day != null || shopMoney != null) {
      final kobo = day != null ? _int(day['in_settled_kobo']) : shopMoney!.kobo;
      final line = day != null ? hourTotals(_maps(day['buckets'])) : paidHourTotals(orders!, now);
      final usual = day != null ? _usual : null;
      final vs = versusUsual(kobo, usual, hour);
      top = TkTopCard(
        semanticsLabel: day != null ? 'Money in today. Open Money' : 'Money in today. Open orders',
        onTap: () => _go(day != null ? MoneyPage(auth: _auth, now: widget.now) : StorefrontOrdersPage(auth: _auth, now: widget.now)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow('Money in today', color: look.limeInk, icon: 'b-money', trailing: 'Now $clock'),
            const SizedBox(height: 6),
            FittedBox(
              fit: BoxFit.scaleDown,
              alignment: Alignment.centerLeft,
              child: TkCountUp(kobo, style: tkFigure(context, size: 42)),
            ),
            if (vs != null) ...[const SizedBox(height: 6), TkTrend(usualWords(vs, now), up: vs.up)],
            const SizedBox(height: 12),
            TkDayLine(
              today: line,
              usual: usual,
              nowHour: hour,
              semanticsLabel: vs == null ? 'Money in through the day so far' : 'Money in through the day so far, against a usual $weekday',
            ),
            const SizedBox(height: 8),
            TkChartKey(
              items: [
                (words: 'Today', color: look.lime, dashed: false),
                if (usual != null) (words: 'A usual $weekday, last 4 weeks', color: t.muted, dashed: true),
              ],
            ),
          ],
        ),
      );
    }

    // Money in hour by hour today, the best hour picked out.
    final dayTotals = day == null ? null : hourTotals(_maps(day['buckets']));
    final perHour = dayTotals == null ? null : [for (var h = 0; h < 24; h++) dayTotals[h + 1] - dayTotals[h]];
    TkHighlight? byHour;
    if (perHour != null && perHour.any((x) => x > 0)) {
      final first = perHour.indexWhere((x) => x > 0), last = perHour.lastIndexWhere((x) => x > 0);
      final from = math.min(8, first), to = math.max(19, math.max(last, _lagos(now).hour));
      var best = first;
      for (var h = first; h <= last; h++) {
        if (perHour[h] > perHour[best]) best = h;
      }
      byHour = TkHighlight(
        wide: true,
        beside: true,
        label: 'Sales by hour',
        icon: 'b-bars',
        color: look.limeInk,
        trailing: payments == 1 ? '1 payment' : '$payments payments',
        value: naira(perHour[best].round()),
        line: 'best hour, ${hourWords(best)}',
        semanticsLabel: 'Best hour today: ${hourWords(best)}, ${naira(perHour[best].round())}',
        chart: TkMiniBars(values: perHour.sublist(from, math.min(24, to + 1)), color: look.lime, picked: best - from),
      );
    }

    final tiles = <TkHighlight>[
      ?byHour,
      if (counts != null)
        TkHighlight(
          label: 'Orders today',
          icon: 'b-box',
          color: look.tealInk,
          value: '${counts.count}',
          line: counts.waiting > 0 ? '${counts.waiting} waiting for you' : 'None waiting for you',
          semanticsLabel: 'Orders today: ${counts.count}, ${counts.waiting > 0 ? '${counts.waiting} waiting for you' : 'none waiting for you'}',
          onTap: () => _go(StorefrontOrdersPage(auth: _auth, now: widget.now)),
        ),
      if (week != null)
        TkHighlight(
          label: 'Jobs this week',
          icon: 'b-bolt',
          color: _int(week['failed']) > 0 ? look.orangeInk : look.blueInk,
          value: '${_int(week['total'])}',
          line: _int(week['failed']) > 0
              ? (_int(week['failed']) == 1 ? '1 needs a look' : '${_int(week['failed'])} need a look')
              : (_int(week['total']) > 0 ? 'All went well' : 'Nothing yet'),
          semanticsLabel: 'Jobs done this week: ${_int(week['total'])}',
          onTap: () => _web('/runs'),
        ),
      if (_workflows != null && counts == null)
        TkHighlight(
          label: 'Working for you',
          icon: 'b-flow',
          color: look.limeInk,
          value: '${live.length}',
          line: live.length == 1 ? 'workflow is live' : 'workflows are live',
          semanticsLabel: 'Working for you: ${live.length} live',
          onTap: () => _web('/workflows'),
        ),
    ];

    return TkScreen(
      title: 'Today',
      back: false,
      onRefresh: _load,
      head: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '${greeting(now)}${first.isNotEmpty ? ', $first' : ''}',
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: tkDisplay(size: 27, lineHeight: 1.1, letterSpacing: -1, color: t.text),
                ),
                const SizedBox(height: 2),
                Text(
                  '${todayWords(now)}${tenant.isNotEmpty ? ' · $tenant' : ''}',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 13.5, lineHeight: 1.3, color: t.muted),
                ),
              ],
            ),
          ),
          if (name.isNotEmpty) ...[const SizedBox(width: 10), TkFace(tkInitials(name), size: 38)],
        ],
      ),
      children: [
        ?top,
        if (tiles.isNotEmpty) TkHighlights(tiles: tiles),
        TkSection(
          title: 'Needs you',
          inset: 56,
          count: needs,
          empty: _approvalsError == null ? 'Nothing needs you right now.' : null,
          children: [
            if (_approvalsError != null) Padding(padding: const EdgeInsets.all(12), child: TkErrorBox(_approvalsError!)),
            for (final a in approvals.take(5))
              () {
                final w = subjectWords(a['subject']);
                final step = _str(a['step_name']);
                final firstPay = a['first_payment'] == true;
                return Row(
                  children: [
                    Expanded(
                      child: TkListRow(
                        title: w.amount != null ? '$step ${w.amount}' : step,
                        sub: '${firstPay ? 'First payment to this account' : _str(a['workflow'])} · asked ${shortWhen(_str(a['requested_at']), now)}',
                        icon: 'b-receipt',
                        square: firstPay ? look.orange : look.violet,
                        onTap: () => _web('/approvals'),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.only(right: 12),
                      child: TkSmallButton(label: 'Approve', semantics: 'Approve $step', primary: true, onPressed: () => _approve(a)),
                    ),
                  ],
                );
              }(),
            if (approvals.length > 5) TkListRow(title: 'See all approvals', icon: 'b-receipt', square: look.violet, onTap: () => _web('/approvals')),
            for (final f in _forms.take(3))
              TkListRow(
                title: _str(f['title']),
                sub: '${_str(f['from']).isEmpty ? 'To fill in' : 'Asked by ${_str(f['from'])}'} · ${shortWhen(_str(f['created_at']), now)}',
                icon: 'forms',
                square: look.blue,
                onTap: () => _go(FormRequestScreen(auth: _auth, id: _str(f['id']), now: widget.now)),
              ),
            for (final p in toMatch.take(3))
              TkListRow(
                title: 'Match ${p['amount_kobo'] == null ? 'a payment' : naira(p['left_kobo'] is num ? _int(p['left_kobo']) : _int(p['amount_kobo']))}',
                sub: '${channelWords(_str(p['channel']))} · ${shortWhen(_str(p['received_at']), now)} · no invoice found yet',
                icon: 'b-cash',
                square: look.orange,
                onTap: () => _go(InvoiceMatchPage(auth: _auth)),
              ),
            for (final o in waitingOrders.take(3))
              () {
                final c = _map(o['customer']);
                final who = [_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'a customer');
                final refund = o['status'] == 'refund_due';
                return TkListRow(
                  title: '${refund ? 'Refund' : 'Order'} from $who',
                  sub: '#${o['reference']} · ${naira(_int(o['total_kobo']))} · ${shortWhen(_str(o['created_at']), now)}',
                  icon: refund ? 'b-refund' : 'b-box',
                  square: refund ? look.orange : look.teal,
                  onTap: () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Today')),
                );
              }(),
          ],
        ),
        if (_runsOK)
          TkSection(
            title: 'Activity',
          inset: 56,
            onSeeAll: () => _web('/runs'),
            seeAllLabel: 'See all runs',
            empty: _runs != null ? 'Nothing has run yet. Publish a workflow and its runs show here.' : null,
            children: [
              if (_runsError != null) Padding(padding: const EdgeInsets.all(12), child: TkErrorBox(_runsError!)),
              if (_runs == null && _runsError == null) const Padding(padding: EdgeInsets.symmetric(horizontal: 6), child: TkLoading(rows: 3, label: 'Loading the latest runs…')),
              for (final r in _runs ?? const <Map<String, dynamic>>[])
                TkListRow(
                  title: '${r['workflow']} ${runWords(_str(r['status']))}',
                  sub: shortWhen(_str(r['started_at']), now),
                  icon: 'b-bolt',
                  square: r['status'] == 'failed' || r['status'] == 'needs_reconciliation' ? look.red : look.blue,
                  onTap: () => _web('/runs/${Uri.encodeComponent(_str(r['id']))}'),
                ),
            ],
          ),
        if (_pages.isNotEmpty) TkSection(title: 'Your pages', inset: 56, children: pinnedPageRows(context, _auth, _pages, backLabel: 'Back to Today')),
        if (_clock != null || (_tasks != null && _tasks!.any((x) => x['state'] == 'open')))
          TkSection(
            title: 'Your team',
          inset: 56,
            children: [
              if (_clock case final c?)
                () {
                  final inNow = _maps(c['in']).length, late = _maps(c['late']).length;
                  return TkListRow(
                    title: 'Staff',
                    sub: ['$inNow in', if (late > 0) '$late late'].join(' · '),
                    icon: 'b-people',
                    square: late > 0 ? look.orange : look.teal,
                    onTap: () => _go(StaffPage(auth: _auth)),
                  );
                }(),
              if (_tasks case final ts?)
                () {
                  final open = ts.where((x) => x['state'] == 'open').toList();
                  final late = open.where((x) => x['late'] == true).length;
                  return TkListRow(
                    title: 'Your tasks',
                    sub: open.isEmpty ? 'None open' : '${open.length} open${late > 0 ? ' · $late late' : ''}',
                    icon: 'b-check',
                    square: late > 0 ? look.orange : look.lime,
                    onTap: () => _go(TasksPage(auth: _auth, now: widget.now)),
                  );
                }(),
            ],
          ),
        if (setup != null)
          TkSection(
            title: 'Finish setting up',
          inset: 56,
            trailing: Padding(
              padding: const EdgeInsets.only(right: 6),
              child: Text('${setup.done} of ${setup.total}', style: tkHint(t)),
            ),
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
                child: Semantics(
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
                            child: Container(color: look.lime),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
              for (final id in setup.left.take(3))
                if (checklist[id] case final c?) TkListRow(title: c.$1, sub: 'On the web', icon: 'b-check', square: look.lime, onTap: () => _web(c.$2)),
              TkListRow(title: 'See every step', icon: 'b-start', square: look.blue, onTap: () => _web('/start')),
            ],
          ),
        if (_workflows != null)
          TkSection(
            title: 'Running for you',
          inset: 56,
            onSeeAll: () => _web('/workflows'),
            seeAllLabel: 'See all workflows',
            empty: 'Nothing yet. Pick a template on the web to get started.',
            children: [
              for (final w in live.take(5))
                () {
                  final n = ((w['recent'] as List?) ?? const []).whereType<num>().fold<num>(0, (a, b) => a + b).toInt();
                  return TkListRow(
                    title: _str(w['name']),
                    sub:
                        '${triggerWords(w['trigger'] is Map ? _map(w['trigger']) : null)} · ${n == 0 ? 'no runs in 2 weeks' : (n == 1 ? 'ran once in 2 weeks' : 'ran $n times in 2 weeks')}',
                    icon: 'b-flow',
                    square: look.teal,
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
