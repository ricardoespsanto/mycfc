# Inventário e matriz de conservação de dados MyCFCoimbra

Estado: **proposta sujeita a validação da Direção, contabilidade, seguradora, federação e apoio jurídico**
Versão: `2026-09-04`

As ações possíveis são: **apagar**, **anonimizar**, **reter com acesso restrito** ou **expirar pelo calendário**. Os prazos abaixo são propostas de produto, não afirmações de obrigação legal. «Fim da relação» significa encerramento de conta e cessação das inscrições/funções relevantes, depois de resolvidas relações com dependentes.

| Categoria/sistema | Conteúdo | Finalidade/base proposta | Destinatários | Prazo proposto | Ação no apagamento | Responsável/controlo |
|---|---|---|---|---|---|---|
| `users` | Nome, nascimento, email/ID menor, estado, hash e versão credencial e conta adulta associada ao menor; a qualidade jurídica do adulto não é atualmente registada/verificada | Conta e relação contratual | Administração autorizada | Relação + 90 dias | Apagar identificadores/credenciais; conservar principal pseudónimo apenas se necessário a registos retidos | Privacidade + TI |
| `member_profiles` | Morada, contactos, identificação, licença FPC | Gestão de membro/federação | Secretaria, FPC quando aplicável | Relação + 90 dias, salvo obrigação documentada | Apagar; restringir apenas campos com fundamento/prazo | Secretaria |
| Saúde/emergência em `member_profiles` | Alergias, condições, medicação, restrições, notas e contacto | Segurança; artigo 9.º a aprovar | Pessoal estritamente necessário/emergência | Enquanto necessário; revisão anual e no fim da época; máximo 30 dias após fim salvo incidente | Apagar; registo do incidente separado e restrito se necessário | Responsável de salvaguarda |
| Foto de perfil/consentimento | Objeto privado, metadados, versão de consentimento | Identificação opcional/consentimento | Públicos autenticados autorizados | Até remoção, retirada ou fim da relação + 30 dias | Apagar objeto e metadados; reter prova mínima de decisão/retirada até prescrição aprovada | Privacidade + TI |
| `consent_forms` | Tipo, versão, hash, decisão, data, concedente, IP, agente | Provar consentimento/declaração | Privacidade/jurídico | Vigência + prazo de defesa a aprovar; rever necessidade de IP/UA após 12 meses | Restringir prova mínima; apagar IP/UA antecipadamente | Privacidade |
| Relações entre conta adulta e menor e credenciais de menores | Conta adulta associada, emissão/recuperação e atores; não existe atualmente campo de qualidade jurídica verificada | Proteção e acesso do menor | Secretaria/privacidade | Durante relação; auditoria pelo prazo de defesa aprovado | Resolver transferência/encerramento; pseudonimizar auditoria | Privacidade |
| Funções, inscrições, equipas/modalidades | Papéis, concessões, épocas, datas | Operação desportiva | Staff autorizado/FPC limitada | Relação e histórico desportivo aprovado | Apagar relação ativa; anonimizar/restringir história conforme obrigação | Secretaria/desporto |
| Eventos e respostas | Agenda, resposta, presença, atores | Organização e segurança | Participantes/staff autorizado | Evento + 2 anos propostos; incidente/seguro conforme prazo legal | Apagar respostas pessoais; anonimizar contagens; restringir incidente | Operações |
| Planos/sessões/publicações | Prescrição, grupo, autores, variações | Prestação desportiva | Atleta, responsável pelo menor autorizado, treinador em âmbito | Relação + 2 anos propostos; metodologia sem pessoa pode persistir | Apagar prescrições individualizadas; pseudonimizar autoria quando possível | Desporto |
| Resultados de treino/atividade/métricas | Resultado, distância, duração, esforço, métricas | Histórico pedido e planeamento | Atleta, responsável pelo menor autorizado, treinador autorizado | Relação + 12 meses propostos, salvo histórico escolhido | Apagar dados individuais; estatística só anonimizada | Desporto/privacidade |
| Ligações e atividades Polar/Garmin | IDs, credenciais cifradas, scopes, cursores, atividades/métricas | Serviço voluntário | Fornecedor escolhido e staff autorizado | Enquanto ligado; apagar local até 30 dias após desligar, salvo pedido mais curto | Revogar/desligar; apagar credenciais, IDs, atividades e correspondências | TI/privacidade |
| Sugestões | Categoria, texto, resposta, autor | Participação e melhoria | Autor e moderação | Encerramento + 2 anos propostos | Apagar identidade e texto pessoal; manter tema anonimizado se útil | Moderação |
| Avisos/notícias/documentos | Conteúdo, autoria, públicos, leituras | Comunicação do clube | Público definido | Conteúdo segundo arquivo; leituras 12 meses propostas | Apagar entregas pessoais; pseudonimizar autor quando lícito | Conteúdo |
| Álbuns/submissões privadas (quando ativados) | Objetos, sujeitos, consentimento, decisões, denúncias | Partilha privada consentida | Público privado/moderação | Pendente 30 dias; rejeitada 30 dias; arquivada 12 meses; removida: objeto imediato/fila | Ocultar imediato; apagar versões; reter decisão mínima 2 anos propostos | Moderação/privacidade |
| Reparações/manutenção | Descrição, imagem, autor, ações | Segurança de equipamento | Administração | Vida do equipamento + 5 anos propostos | Anonimizar autor; apagar imagem pessoal/desnecessária | Frota |
| Auditorias imutáveis | Ator/sujeito, campos alterados, estados JSON | Segurança, responsabilização | Administração restrita | 2 anos propostos; categorias legais conforme prazo próprio | Pseudonimizar referências; remover valores pessoais de JSON; restringir exceções | Segurança |
| Sessões | Token e dados serializados | Autenticação | Aplicação | Expiração configurada; limpeza contínua | Revogar/purgar no início do apagamento | TI |
| Verificação/reset | Email, digest, payload cifrado, estados | Segurança da conta | Aplicação/SMTP | Token expirado/consumido + 30 dias propostos | Apagar com conta ou antes | TI |
| Outbox/email | Destinatário derivado, tipo, payload cifrado, erro | Entrega e prova operacional | SMTP | Enviado/cancelado 90 dias; falha 180 dias propostos | Apagar conteúdo/destinatário; manter métrica anónima | TI |
| Registos aplicação/proxy/sistema | IP, rota, timestamp, erros técnicos | Segurança/operação | Operadores, Hetzner/Cloudflare conforme serviço | Acesso 30 dias; segurança 90 dias; incidente isolado pelo prazo necessário | Expirar; restringir incidente; nunca copiar dados de perfil | Segurança/operador |
| Objetos MinIO/S3 | Fotos de perfil, reparações/equipamento e futuros álbuns | Funcionalidade respetiva | Aplicação/operadores limitados | Igual ao registo proprietário; versões não correntes ≤30 dias propostas | Fila durável, todas as versões, ausência conta como sucesso | TI |
| Backups PostgreSQL | Cópia cifrada da base | Continuidade | Operadores autorizados/AWS conforme destino | Diário 30 dias e mensal 365 dias (configuração atual a aprovar) | Não reescrever; isolar, expirar e reaplicar tombstones antes de servir; corrigir versões S3 não correntes sem expiração | Operações |
| Backups/snapshots Hetzner | Cópia de disco/servidor e metadados | Continuidade | Operadores/Hetzner | Confirmar sete cópias diárias rotativas e quaisquer snapshots manuais | Isolar/expirar; incluir no ensaio de ressurreição | Operações |
| Artefactos CI/deployment | Relatórios, capturas e logs; dados reais são proibidos | Qualidade/operação | GitHub/operadores | Artefactos CI 14 dias; alguns ensaios 30 dias; CloudWatch deployment 30 dias, conforme configuração atual | Expirar; impedir dados pessoais de produção na origem | Engenharia |
| Email/SMTP e destinatários externos | Endereço e mensagem | Comunicar | Prestador SMTP | Contrato/prazo do prestador a confirmar | Notificar/apagar quando exigido e possível | Operações |
| Cloudflare/Turnstile | IP e sinais técnicos antifraude/rede | Segurança | Cloudflare | Confirmar produto, região, contrato e prazo | Pedido/notificação conforme papel e contrato | Segurança |
| Hetzner | Dados alojados e metadados de infraestrutura | Alojamento | Hetzner | Contrato e backups do serviço a confirmar | Eliminar recursos/cópias segundo contrato | Operações |
| AWS | DNS, imagens, email e/ou backups conforme configuração | Infraestrutura | AWS | Por serviço e contrato | Aplicar eliminação/lifecycle; conservar prova não pessoal | Operações |
| FPC/organizadores/seguradora/autoridades | Inscrição, resultados, incidente ou obrigação | Regras/lei/seguro | Entidade concreta | Prazo da entidade e obrigação a documentar | Artigo 19.º quando aplicável; registar resposta | Secretaria/privacidade |

## Lacunas que bloqueiam aprovação

- confirmar fundamento do artigo 9.º e necessidade de cada campo médico;
- confirmar prazos fiscais, de quotas/pagamentos (quando existirem), seguros, acidentes, federação e responsabilidade civil;
- inventariar nomes/durações reais de cookies, configuração Cloudflare/Turnstile e logs;
- confirmar subprocessadores, localizações, contratos, transferências e prazos de Hetzner, Cloudflare, AWS e SMTP;
- decidir se 365 dias de backup mensal são necessários e garantir que o ledger de apagamento cobre o backup restaurável mais antigo;
- corrigir a regra do bucket versionado: a expiração atual de 30/365 dias não elimina versões não correntes e pode conservá-las indefinidamente;
- a eliminação comum de objetos em bucket versionado não remove necessariamente versões antigas (até 90 dias na configuração conhecida) e a permissão atual pode não permitir eliminação completa; fechar a lacuna antes de prometer conclusão;
- o ensaio de restauro atual verifica a base mas ainda não reaplica tombstones; #111 tem de implementar e provar esse passo;
- inspecionar todos os JSON de auditoria antes de permitir pseudonimização;
- aprovar prazo da prova mínima de consentimento e dos processos de direitos;
- atualizar esta matriz antes de ativar pagamentos, transporte, álbuns, Polar/Garmin ou importação de resultados.
