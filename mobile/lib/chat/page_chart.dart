// A page's chart (pages G4, docs/pages.md; web/src/pages/PageG4.tsx):
// bars or a line drawn natively from what the server worked out. Every
// figure is the server's: the points, each one's place on the scale
// (share, 0 at its foot, 1 at its top), where nought is, the scale's marks,
// the table's heads and the chart's words. Nothing is added up here.
//
// As the money screen's bars (money_page.dart, MoneyBars): pressing a bar
// (or a point) says its figures on the line above the chart, read out as
// it changes; the chart itself is named by its words; See the figures
// lists every one. Its colour is the theme's chart colour (the web's
// --chart-in), checked against the card in both themes.

import 'dart:math' as math;

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/kit.dart' show TkLink, tkHint;
import '../kit/mobile.dart';
import '../theme/look.dart';
import '../theme/theme.dart';

/// Where the plot starts, after the scale's marks.
const _left = 44.0;
const _plot = 140.0;

/// A chart part, its plot on a panel.
class PageChart extends StatefulWidget {
  const PageChart({super.key, required this.b});
  final PageBlock b;

  @override
  State<PageChart> createState() => _PageChartState();
}

class _PageChartState extends State<PageChart> {
  late int _picked = math.max(0, (widget.b.points?.length ?? 0) - 1);
  bool _figures = false;

  @override
  void didUpdateWidget(PageChart old) {
    super.didUpdateWidget(old);
    final n = widget.b.points?.length ?? 0;
    if (_picked >= n) _picked = math.max(0, n - 1);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = widget.b;
    final points = b.points ?? const <PageChartPoint>[];
    final body = <Widget>[];
    if (b.error != null || b.working != null) {
      body.add(Text(b.error ?? b.working!, style: tkHint(t)));
    } else if (points.isEmpty) {
      body.add(Text(b.empty ?? 'Nothing to show yet', style: tkHint(t)));
    } else {
      final at = points[_picked.clamp(0, points.length - 1)];
      body.addAll([
        Semantics(
          liveRegion: true,
          child: Text.rich(
            TextSpan(children: [
              TextSpan(text: '${at.long}  ', style: tkText(size: 14, weight: 700, color: t.text)),
              TextSpan(text: at.shown, style: tkHint(t)),
            ]),
          ),
        ),
        const SizedBox(height: 10),
        Semantics(
          label: '${b.title == null ? '' : '${b.title}. '}${b.summary ?? ''}',
          container: true,
          explicitChildNodes: true,
          child: SizedBox(
            height: _plot,
            child: Stack(
              children: [
                Positioned.fill(
                  child: ExcludeSemantics(
                    child: CustomPaint(
                      painter: _ChartPainter(
                        kind: b.kind ?? 'bars',
                        points: points,
                        ticks: b.ticks ?? const <PageBlockTicksItem>[],
                        zero: b.zero ?? 0,
                        picked: _picked,
                        mark: t.chartIn,
                        soft: t.tkSoft,
                        grid: TkLook.of(context).line,
                        card: TkLook.of(context).card,
                        muted: t.muted,
                      ),
                    ),
                  ),
                ),
                // Each bar pressed, where each is wide enough for a finger
                // (48 px); narrower ones are in See the figures.
                Positioned.fill(
                  left: _left,
                  child: LayoutBuilder(
                    builder: (context, c) => c.maxWidth / points.length < 48
                        ? const SizedBox.shrink()
                        : Row(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              for (var i = 0; i < points.length; i++)
                                Expanded(
                                  child: Semantics(
                                    button: true,
                                    selected: i == _picked,
                                    label: '${points[i].long}: ${points[i].shown}',
                                    excludeSemantics: true,
                                    onTap: () => setState(() => _picked = i),
                                    child: GestureDetector(behavior: HitTestBehavior.opaque, onTap: () => setState(() => _picked = i)),
                                  ),
                                ),
                            ],
                          ),
                  ),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 4),
        ExcludeSemantics(
          child: Padding(
            padding: const EdgeInsets.only(left: _left),
            child: Row(
              children: [
                for (var i = 0; i < points.length; i++)
                  Expanded(
                    child: Text(
                      points[i].tick ? points[i].label : '',
                      textAlign: TextAlign.center,
                      maxLines: 1,
                      overflow: TextOverflow.clip,
                      style: tkText(size: 11, weight: i == _picked ? 700 : 500, color: i == _picked ? t.text : t.muted),
                    ),
                  ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 10),
        Text(b.summary ?? '', style: tkHint(t)),
        if (b.over != null) Text(b.over!, style: tkHint(t)),
        const SizedBox(height: 6),
        Align(
          alignment: Alignment.centerLeft,
          child: Semantics(
            expanded: _figures,
            child: TkLink(label: _figures ? 'Hide the figures' : 'See the figures', bold: true, onTap: () => setState(() => _figures = !_figures)),
          ),
        ),
        if (_figures)
          for (final p in points)
            MergeSemantics(
              child: Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Row(
                  children: [
                    Expanded(child: Text(p.long, style: tkText(size: 14, color: t.text))),
                    Text(p.shown, style: tkText(size: 14, weight: 600, color: t.text)),
                  ],
                ),
              ),
            ),
      ]);
    }
    return TkSection(
      title: b.title,
      inset: 16,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: body),
        ),
      ],
    );
  }
}

/// Draws the scale's marks and lines, and the bars or the line, at the
/// places the server gave.
class _ChartPainter extends CustomPainter {
  _ChartPainter({
    required this.kind,
    required this.points,
    required this.ticks,
    required this.zero,
    required this.picked,
    required this.mark,
    required this.soft,
    required this.grid,
    required this.card,
    required this.muted,
  });
  final String kind;
  final List<PageChartPoint> points;
  final List<PageBlockTicksItem> ticks;
  final double zero;
  final int picked;
  final Color mark, soft, grid, card, muted;

  @override
  void paint(Canvas canvas, Size size) {
    const top = 6.0;
    final h = size.height - top - 2;
    final w = size.width - _left;
    double y(double share) => top + h - share * h;
    final line = Paint()
      ..color = grid
      ..strokeWidth = 1;
    for (final tk in ticks) {
      final yy = y(tk.share);
      canvas.drawLine(Offset(_left, yy), Offset(size.width, yy), line);
      final tp = TextPainter(
        text: TextSpan(text: tk.shown, style: tkText(size: 11, color: muted)),
        textDirection: TextDirection.ltr,
        maxLines: 1,
      )..layout(maxWidth: _left - 6);
      tp.paint(canvas, Offset(_left - 6 - tp.width, yy - tp.height / 2));
    }
    final base = Paint()
      ..color = muted.withValues(alpha: 0.6)
      ..strokeWidth = 1;
    canvas.drawLine(Offset(_left, y(zero)), Offset(size.width, y(zero)), base);
    if (points.isEmpty) return;
    final band = w / points.length;
    // The one pressed: a soft band behind it, as the money screen's.
    canvas.drawRRect(
      RRect.fromRectAndRadius(Rect.fromLTWH(_left + band * picked + 1, top - 4, band - 2, h + 6), const Radius.circular(6)),
      Paint()..color = soft,
    );
    final fill = Paint()..color = mark;
    if (kind == 'line') {
      final path = Path();
      for (var i = 0; i < points.length; i++) {
        final o = Offset(_left + band * i + band / 2, y(points[i].share));
        if (i == 0) {
          path.moveTo(o.dx, o.dy);
        } else {
          path.lineTo(o.dx, o.dy);
        }
      }
      canvas.drawPath(
        path,
        Paint()
          ..color = mark
          ..style = PaintingStyle.stroke
          ..strokeWidth = 2
          ..strokeJoin = StrokeJoin.round
          ..strokeCap = StrokeCap.round,
      );
      for (var i = 0; i < points.length; i++) {
        final o = Offset(_left + band * i + band / 2, y(points[i].share));
        canvas.drawCircle(o, i == picked ? 7 : 6, Paint()..color = card);
        canvas.drawCircle(o, i == picked ? 5 : 4, fill);
      }
      return;
    }
    final barW = math.max(4.0, math.min(24.0, band - 8));
    for (var i = 0; i < points.length; i++) {
      final cx = _left + band * i + band / 2;
      final a = y(points[i].share), b = y(zero);
      final topY = math.min(a, b), height = (a - b).abs();
      if (height <= 0) continue;
      final r = Radius.circular(math.min(4, math.min(barW / 2, height)));
      final up = points[i].share >= zero;
      canvas.drawRRect(
        RRect.fromRectAndCorners(Rect.fromLTWH(cx - barW / 2, topY, barW, height), topLeft: up ? r : Radius.zero, topRight: up ? r : Radius.zero, bottomLeft: up ? Radius.zero : r, bottomRight: up ? Radius.zero : r),
        fill,
      );
    }
  }

  @override
  bool shouldRepaint(_ChartPainter old) =>
      old.picked != picked || old.points != points || old.kind != kind || old.mark != mark || old.card != card || old.ticks != ticks || old.zero != zero;
}
