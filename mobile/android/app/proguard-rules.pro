# ML Kit text recognition (photo text, I1) uses only the Latin script.
# The plugin refers to the other scripts' options, whose packages the app
# does not include; R8 is told they are missing on purpose.
-dontwarn com.google.mlkit.vision.text.chinese.**
-dontwarn com.google.mlkit.vision.text.devanagari.**
-dontwarn com.google.mlkit.vision.text.japanese.**
-dontwarn com.google.mlkit.vision.text.korean.**
