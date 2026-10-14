# kotlinx.serialization: keep generated serializers of the shared models
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**
-keepclassmembers class io.github.xattribution.burrow.** {
    *** Companion;
}
-keepclasseswithmembers class io.github.xattribution.burrow.** {
    kotlinx.serialization.KSerializer serializer(...);
}
# BouncyCastle: only the lightweight ML-KEM and X25519 classes are used, by name
-keep class org.bouncycastle.crypto.generators.MLKEMKeyPairGenerator { *; }
-keep class org.bouncycastle.crypto.kems.MLKEMExtractor { *; }
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
-dontwarn javax.naming.**

# smbj (SMB shares): its event bus (mbassador) finds handlers by reflection, and
# optional Kerberos and expression-language support reference classes Android lacks
-keep class com.hierynomus.** { *; }
-keep class net.engio.mbassy.** { *; }
-keepattributes *Annotation*
-dontwarn javax.el.**
-dontwarn org.ietf.jgss.**
-dontwarn org.slf4j.**
